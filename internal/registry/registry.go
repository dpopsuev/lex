package registry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dpopsuev/lex/internal/frontmatter"
)

type Source struct {
	URL       string         `json:"url"`
	Ref       string         `json:"ref,omitempty"`
	Priority  int            `json:"priority"`
	LocalPath string         `json:"local_path"`
	AddedAt   time.Time      `json:"added_at"`
	SyncedAt  time.Time      `json:"synced_at"`
	Hash      string         `json:"hash"`
	Config    *LexiconConfig `json:"config,omitempty"`
}

type Artifact struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Path     string   `json:"path"`
	Source   string   `json:"source"`
	Priority int      `json:"priority"`
	ID       string   `json:"id,omitempty"`
	Title    string   `json:"title,omitempty"`
	Labels   []string `json:"labels,omitempty"`
}

type Registry struct {
	root string // ~/.lex
}

func DefaultRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".lex")
}

func New(root string) *Registry {
	return &Registry{root: root}
}

func (r *Registry) SourcesPath() string {
	return filepath.Join(r.root, "sources.json")
}

func (r *Registry) lexiconDir(hash string) string {
	return filepath.Join(r.root, "lexicons", hash)
}

func (r *Registry) Load() ([]Source, error) {
	data, err := os.ReadFile(r.SourcesPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sources []Source
	if err := json.Unmarshal(data, &sources); err != nil {
		return nil, err
	}
	return sources, nil
}

func (r *Registry) save(sources []Source) error {
	os.MkdirAll(r.root, 0o755)
	data, err := json.MarshalIndent(sources, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.SourcesPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.SourcesPath())
}

// withLock acquires an advisory exclusive lock for the duration of fn.
// Readers (Load) remain lock-free; atomic rename ensures they see consistent state.
func (r *Registry) withLock(fn func() error) error {
	os.MkdirAll(r.root, 0o755)
	lockPath := r.SourcesPath() + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open lock: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (r *Registry) Add(_ context.Context, gitURL, ref string, priority int) (*Source, error) {
	hash := urlHash(gitURL)
	localPath := r.lexiconDir(hash)

	if err := shallowClone(gitURL, ref, localPath); err != nil {
		return nil, fmt.Errorf("clone lexicon: %w", err)
	}

	src := &Source{
		URL:       gitURL,
		Ref:       ref,
		Priority:  priority,
		LocalPath: localPath,
		AddedAt:   time.Now(),
		SyncedAt:  time.Now(),
		Hash:      hash,
	}

	err := r.withLock(func() error {
		sources, _ := r.Load()
		found := false
		for i, s := range sources {
			if s.Hash == hash {
				sources[i] = *src
				found = true
				break
			}
		}
		if !found {
			sources = append(sources, *src)
		}
		return r.save(sources)
	})
	if err != nil {
		return nil, fmt.Errorf("save sources: %w", err)
	}
	return src, nil
}

func (r *Registry) Sync(_ context.Context) (int, error) {
	sources, err := r.Load()
	if err != nil {
		return 0, err
	}

	synced := 0
	for i, src := range sources {
		if err := shallowClone(src.URL, src.Ref, src.LocalPath); err != nil {
			continue
		}
		sources[i].SyncedAt = time.Now()
		synced++
	}
	if err := r.withLock(func() error { return r.save(sources) }); err != nil {
		return synced, err
	}
	return synced, nil
}

func (r *Registry) Remove(_ context.Context, url string) error {
	var removed *Source
	err := r.withLock(func() error {
		sources, err := r.Load()
		if err != nil {
			return err
		}
		var kept []Source
		for _, s := range sources {
			if s.URL == url {
				removed = &s
				continue
			}
			kept = append(kept, s)
		}
		if removed == nil {
			return fmt.Errorf("lexicon source not found: %s", url)
		}
		return r.save(kept)
	})
	if err != nil {
		return err
	}
	if removed != nil && removed.LocalPath != "" {
		os.RemoveAll(removed.LocalPath)
	}
	return nil
}

// DiscoverArtifacts walks a cloned lexicon directory and returns rules,
// templates, and skills found inside, with frontmatter metadata.
func DiscoverArtifacts(root, sourceURL string, priority int) []Artifact {
	var artifacts []Artifact
	for _, dir := range []string{"rules", "templates", "skills"} {
		dirPath := filepath.Join(root, dir)
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				skillPath := filepath.Join(dirPath, e.Name(), "SKILL.md")
				if _, err := os.Stat(skillPath); err == nil {
					a := Artifact{
						Name:     e.Name(),
						Type:     "skill",
						Path:     skillPath,
						Source:   sourceURL,
						Priority: priority,
					}
					enrichFromFrontmatter(&a)
					artifacts = append(artifacts, a)
				}
				continue
			}
			ext := filepath.Ext(e.Name())
			if ext == ".md" || ext == ".mdc" || ext == ".yaml" || ext == ".yml" {
				a := Artifact{
					Name:     strings.TrimSuffix(e.Name(), ext),
					Type:     dir[:len(dir)-1],
					Path:     filepath.Join(dirPath, e.Name()),
					Source:   sourceURL,
					Priority: priority,
				}
				enrichFromFrontmatter(&a)
				artifacts = append(artifacts, a)
			}
		}
	}
	return artifacts
}

func enrichFromFrontmatter(a *Artifact) {
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return
	}
	fm, _ := frontmatter.Parse(string(data))
	if id := fm["id"]; id != "" {
		a.ID = id
	}
	if title := fm["title"]; title != "" {
		a.Title = title
	}
	if labels := fm.Labels(); len(labels) > 0 {
		a.Labels = labels
	}
}

func shallowClone(gitURL, ref, dest string) error {
	os.RemoveAll(dest)
	os.MkdirAll(filepath.Dir(dest), 0o755)

	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, gitURL, dest)

	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %s: %w", string(out), err)
	}
	return nil
}

func urlHash(url string) string {
	h := sha256.Sum256([]byte(url))
	return fmt.Sprintf("%x", h[:8])
}
