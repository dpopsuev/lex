package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DefaultPriority int      `yaml:"default_priority"`
	CacheDir        string   `yaml:"cache_dir"`
	Enabled         bool     `yaml:"enabled"`
	Labels          []string `yaml:"labels,omitempty"`
}

func DefaultConfig() *Config {
	return &Config{
		DefaultPriority: 50,
		CacheDir:        "~/.lex/cache",
		Enabled:         true,
	}
}

func configPath(root string) string {
	if root != "" {
		return filepath.Join(root, "config.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".lex", "config.yaml")
}

func expandPath(p string) (string, error) {
	if len(p) > 0 && p[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, err
		}
		if len(p) == 1 || p[1] == '/' {
			return filepath.Join(home, p[1:]), nil
		}
	}
	return p, nil
}

func (c *Config) ExpandPaths() error {
	expanded, err := expandPath(c.CacheDir)
	if err != nil {
		return err
	}
	c.CacheDir = expanded
	return nil
}

func Load(root string) (*Config, error) {
	path := configPath(root)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfig()
		_ = cfg.ExpandPaths()
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.DefaultPriority == 0 {
		cfg.DefaultPriority = 50
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = "~/.lex/cache"
	}
	_ = cfg.ExpandPaths()
	return &cfg, nil
}

func (c *Config) Save(root string) error {
	path := configPath(root)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SetConfig updates a single config field by key. Supported keys:
// default_priority, cache_dir, enabled, labels (comma-separated). Unknown keys are ignored.
func (c *Config) SetConfig(key, value string) error {
	switch key {
	case "default_priority":
		if n, err := strconv.Atoi(value); err == nil {
			c.DefaultPriority = n
		}
	case "cache_dir":
		c.CacheDir = value
	case "enabled":
		c.Enabled = strings.EqualFold(value, "true") || value == "1"
	case "labels":
		if value == "" {
			c.Labels = nil
		} else {
			c.Labels = strings.Split(value, ",")
			for i := range c.Labels {
				c.Labels[i] = strings.TrimSpace(c.Labels[i])
			}
		}
	}
	return nil
}
