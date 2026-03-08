package registry

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type LexiconConfig struct {
	Name        string         `yaml:"name"        json:"name"`
	Description string         `yaml:"description" json:"description"`
	Version     string         `yaml:"version"     json:"version"`
	Defaults    ConfigDefaults `yaml:"defaults"    json:"defaults"`
	Routing     []RoutingRule  `yaml:"routing"     json:"routing"`
}

type ConfigDefaults struct {
	Priority int `yaml:"priority" json:"priority"`
}

type RoutingRule struct {
	Match  MatchCriteria `yaml:"match"                 json:"match"`
	Globs  []string      `yaml:"globs,omitempty"       json:"globs,omitempty"`
	Always bool          `yaml:"always_apply,omitempty" json:"always_apply,omitempty"`
}

type MatchCriteria struct {
	Labels []string `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// LoadLexiconConfig reads and parses lexicon.yaml from a cloned lexicon root.
// Returns nil (no error) if the file doesn't exist.
func LoadLexiconConfig(root string) (*LexiconConfig, error) {
	p := filepath.Join(root, "lexicon.yaml")
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg LexiconConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
