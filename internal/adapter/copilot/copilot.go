package copilot

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dpopsuev/ordo/adapter"
	"github.com/dpopsuev/ordo/rule"
)

func init() { adapter.Register(&Adapter{}) }

// Adapter reads .github/copilot-instructions.md and .github/copilot/*.md.
type Adapter struct{}

func (a *Adapter) Name() string { return "copilot" }

func (a *Adapter) Detect(root string) bool {
	if _, err := os.Stat(filepath.Join(root, ".github", "copilot-instructions.md")); err == nil {
		return true
	}
	dir := filepath.Join(root, ".github", "copilot")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			return true
		}
	}
	return false
}

func (a *Adapter) Load(root string) ([]rule.Rule, error) {
	out := make([]rule.Rule, 0, 1)

	if data, err := os.ReadFile(filepath.Join(root, ".github", "copilot-instructions.md")); err == nil {
		body := strings.TrimSpace(string(data))
		if body != "" {
			out = append(out, rule.Rule{
				Name:     "copilot-instructions",
				Kind:     "rule",
				Source:   "local",
				Adapter:  "copilot",
				Content:  body,
				Scope:    "project",
				Priority: 50,
				Triggers: []rule.Trigger{{Type: rule.TriggerAlways}},
			})
		}
	}

	dir := filepath.Join(root, ".github", "copilot")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out, nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		body := strings.TrimSpace(string(data))
		if body == "" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		out = append(out, rule.Rule{
			Name:     name,
			Kind:     "rule",
			Source:   "local",
			Adapter:  "copilot",
			Content:  body,
			Scope:    "project",
			Priority: 50,
			Triggers: []rule.Trigger{{Type: rule.TriggerAlways}},
		})
	}
	return out, nil
}
