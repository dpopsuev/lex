package claude

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dpopsuev/lex/internal/adapter"
	"github.com/dpopsuev/lex/internal/rule"
)

func init() { adapter.Register(&Adapter{}) }

// Adapter reads CLAUDE.md as a rule source.
type Adapter struct{}

func (a *Adapter) Name() string { return "claude" }

func (a *Adapter) Detect(root string) bool {
	_, err := os.Stat(filepath.Join(root, "CLAUDE.md"))
	return err == nil
}

func (a *Adapter) Load(root string) ([]rule.Rule, error) {
	data, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		return nil, nil
	}
	body := strings.TrimSpace(string(data))
	if body == "" {
		return nil, nil
	}
	return []rule.Rule{{
		Name:     "CLAUDE.md",
		Kind:     "rule",
		Source:   "local",
		Adapter:  "claude",
		Content:  body,
		Scope:    "project",
		Priority: 50,
		Triggers: []rule.Trigger{{Type: rule.TriggerAlways}},
	}}, nil
}
