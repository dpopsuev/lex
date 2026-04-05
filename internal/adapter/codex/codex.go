package codex

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dpopsuev/ordo/adapter"
	"github.com/dpopsuev/ordo/rule"
)

func init() { adapter.Register(&Adapter{}) }

// Adapter reads AGENTS.md as a rule source.
type Adapter struct{}

func (a *Adapter) Name() string { return "codex" }

func (a *Adapter) Detect(root string) bool {
	_, err := os.Stat(filepath.Join(root, "AGENTS.md"))
	return err == nil
}

func (a *Adapter) Load(root string) ([]rule.Rule, error) {
	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		return nil, nil
	}
	body := strings.TrimSpace(string(data))
	if body == "" {
		return nil, nil
	}
	return []rule.Rule{{
		Name:     "AGENTS.md",
		Kind:     "rule",
		Source:   "local",
		Adapter:  "codex",
		Content:  body,
		Scope:    "project",
		Priority: 50,
		Triggers: []rule.Trigger{{Type: rule.TriggerAlways}},
	}}, nil
}
