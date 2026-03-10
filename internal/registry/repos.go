package registry

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// RepoConfig is the per-source config stored in ~/.lex/repos.d/*.yaml
type RepoConfig struct {
	URL      string   `yaml:"url"`
	Enabled  bool     `yaml:"enabled"`
	Priority int      `yaml:"priority"`
	Ref      string   `yaml:"ref,omitempty"`
	Labels   []string `yaml:"labels,omitempty"`
}

func repoFilename(url string) string {
	h := sha256.Sum256([]byte(url))
	return "repo-" + fmt.Sprintf("%x", h[:8]) + ".yaml"
}

func (r *Registry) loadRepo(url string) (*RepoConfig, error) {
	dir := r.ReposDir()
	path := filepath.Join(dir, repoFilename(url))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg RepoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (r *Registry) saveRepo(cfg *RepoConfig) error {
	dir := r.ReposDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, repoFilename(cfg.URL))
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (r *Registry) listRepos() ([]RepoConfig, error) {
	dir := r.ReposDir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var configs []RepoConfig
	for _, e := range entries {
		if e.IsDir() || (filepath.Ext(e.Name()) != ".yaml" && filepath.Ext(e.Name()) != ".yml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg RepoConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			continue
		}
		if cfg.URL != "" {
			configs = append(configs, cfg)
		}
	}
	return configs, nil
}

// SaveRepoForTest writes a repo config to repos.d. Used by tests.
func (r *Registry) SaveRepoForTest(rc *RepoConfig) error {
	return r.saveRepo(rc)
}

func (r *Registry) removeRepoFile(url string) error {
	path := filepath.Join(r.ReposDir(), repoFilename(url))
	return os.Remove(path)
}

// repoToSource converts RepoConfig + clone metadata to Source
func repoToSource(rc RepoConfig, localPath, hash string, addedAt, syncedAt time.Time) Source {
	prio := rc.Priority
	if prio == 0 {
		prio = 50
	}
	return Source{
		URL:       rc.URL,
		Ref:       rc.Ref,
		Priority:  prio,
		Enabled:   rc.Enabled,
		Labels:    rc.Labels,
		LocalPath: localPath,
		AddedAt:   addedAt,
		SyncedAt:  syncedAt,
		Hash:      hash,
	}
}
