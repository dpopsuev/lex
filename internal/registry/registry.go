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
	Enabled   bool           `json:"enabled"`
	Labels    []string       `json:"labels,omitempty"`
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

func (r *Registry) Root() string {
	return r.root
}

func (r *Registry) SourcesPath() string {
	return filepath.Join(r.root, "sources.json")
}

func (r *Registry) ReposDir() string {
	return filepath.Join(r.root, "repos.d")
}

func (r *Registry) lexiconDir(hash string) string {
	return filepath.Join(r.root, "lexicons", hash)
}

// LexiconDirForURL returns the local path for a lexicon URL (used by tests).
func (r *Registry) LexiconDirForURL(url string) string {
	return r.lexiconDir(urlHash(url))
}

func (r *Registry) Load() ([]Source, error) {
	// Prefer repos.d; migrate from sources.json if repos.d is empty
	repos, err := r.listRepos()
	if err != nil {
		return nil, err
	}
	if len(repos) > 0 {
		return r.sourcesFromRepos(repos)
	}
	// Backward compat: load from sources.json and migrate to repos.d
	sources, err := r.loadFromSourcesJSON()
	if err != nil || len(sources) == 0 {
		return sources, err
	}
	// Migrate each source to repos.d
	for _, s := range sources {
		rc := RepoConfig{
			URL:      s.URL,
			Ref:      s.Ref,
			Priority: s.Priority,
			Enabled:  true,
			Labels:   s.Labels,
		}
		if rc.Priority == 0 {
			rc.Priority = 50
		}
		_ = r.saveRepo(&rc)
	}
	return sources, nil
}

func (r *Registry) loadFromSourcesJSON() ([]Source, error) {
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
	for i := range sources {
		sources[i].Enabled = true // sources.json had no Enabled; default true for migrated
		if sources[i].Priority == 0 {
			sources[i].Priority = 50
		}
	}
	return sources, nil
}

func (r *Registry) sourcesFromRepos(repos []RepoConfig) ([]Source, error) {
	var sources []Source
	for _, rc := range repos {
		hash := urlHash(rc.URL)
		localPath := r.lexiconDir(hash)
		// Check if clone exists; if not, source is registered but not yet cloned
		addedAt := time.Time{}
		syncedAt := time.Time{}
		if fi, err := os.Stat(localPath); err == nil && fi.IsDir() {
			addedAt = fi.ModTime()
			syncedAt = fi.ModTime()
		}
		src := repoToSource(rc, localPath, hash, addedAt, syncedAt)
		sources = append(sources, src)
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

	if priority == 0 {
		priority = 50
	}
	now := time.Now()
	src := &Source{
		URL:       gitURL,
		Ref:       ref,
		Priority:  priority,
		Enabled:   true,
		LocalPath: localPath,
		AddedAt:   now,
		SyncedAt:  now,
		Hash:      hash,
	}

	rc := RepoConfig{
		URL:      gitURL,
		Ref:      ref,
		Priority: priority,
		Enabled:  true,
	}
	if err := r.saveRepo(&rc); err != nil {
		return nil, fmt.Errorf("save repo config: %w", err)
	}
	return src, nil
}

func (r *Registry) Sync(_ context.Context) (int, error) {
	sources, err := r.Load()
	if err != nil {
		return 0, err
	}

	synced := 0
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		if err := shallowClone(src.URL, src.Ref, src.LocalPath); err != nil {
			continue
		}
		synced++
	}
	return synced, nil
}

func (r *Registry) EnableSource(url string) error {
	return r.setSourceEnabled(url, true)
}

func (r *Registry) DisableSource(url string) error {
	return r.setSourceEnabled(url, false)
}

func (r *Registry) setSourceEnabled(url string, enabled bool) error {
	rc, err := r.loadRepo(url)
	if err != nil {
		return err
	}
	if rc == nil {
		return fmt.Errorf("lexicon source not found: %s", url)
	}
	rc.Enabled = enabled
	return r.saveRepo(rc)
}

func (r *Registry) SetSourcePriority(url string, priority int) error {
	rc, err := r.loadRepo(url)
	if err != nil {
		return err
	}
	if rc == nil {
		return fmt.Errorf("lexicon source not found: %s", url)
	}
	rc.Priority = priority
	return r.saveRepo(rc)
}

func (r *Registry) Remove(_ context.Context, url string) error {
	sources, err := r.Load()
	if err != nil {
		return err
	}
	var removed *Source
	for _, s := range sources {
		if s.URL == url {
			removed = &s
			break
		}
	}
	if removed == nil {
		return fmt.Errorf("lexicon source not found: %s", url)
	}
	if err := r.removeRepoFile(url); err != nil && !os.IsNotExist(err) {
		return err
	}
	if removed.LocalPath != "" {
		os.RemoveAll(removed.LocalPath)
	}
	return nil
}

// DiscoverArtifacts walks a cloned lexicon directory and returns rules,
// templates, and skills found inside, with frontmatter metadata.
// Supports both legacy layout (rules/, skills/, templates/) and the
// v0.5.0 generic/ layout (generic/rules/, generic/skills/).
func DiscoverArtifacts(root, sourceURL string, priority int) []Artifact {
	var artifacts []Artifact

	// Check for v0.5.0 generic/ layout first; fall back to legacy root.
	base := root
	if fi, err := os.Stat(filepath.Join(root, "generic")); err == nil && fi.IsDir() {
		base = filepath.Join(root, "generic")
	}

	for _, dir := range []string{"rules", "templates", "skills"} {
		dirPath := filepath.Join(base, dir)
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
