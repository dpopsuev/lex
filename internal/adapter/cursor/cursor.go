package cursor

import (
	"path/filepath"
	"strings"

	"github.com/dpopsuev/lex/internal/adapter"
	"github.com/dpopsuev/lex/internal/cursor"
	"github.com/dpopsuev/lex/internal/rule"
)

func init() { adapter.Register(&Adapter{}) }

// Adapter reads .cursor/rules/*.mdc and .cursor/skills/*/SKILL.md.
type Adapter struct{}

func (a *Adapter) Name() string { return "cursor" }

func (a *Adapter) Detect(root string) bool {
	rules, _ := cursor.ReadRules(root)
	skills, _ := cursor.ReadSkills(root)
	return len(rules) > 0 || len(skills) > 0
}

func (a *Adapter) Load(root string) ([]rule.Rule, error) {
	var out []rule.Rule

	rules, _ := cursor.ReadRules(root)
	for _, r := range rules {
		name := strings.TrimSuffix(filepath.Base(r.Path), filepath.Ext(r.Path))
		rr := rule.Rule{
			Name:    name,
			Kind:    "rule",
			Source:  "local",
			Adapter: "cursor",
			Content: r.Body,
			Scope:   "project",
			Priority: 50,
			Globs:   r.Globs,
		}
		if r.AlwaysApply {
			rr.Triggers = append(rr.Triggers, rule.Trigger{Type: rule.TriggerAlways})
		}
		for _, g := range r.Globs {
			rr.Triggers = append(rr.Triggers, rule.Trigger{Type: rule.TriggerFileGlob, Pattern: g})
		}
		out = append(out, rr)
	}

	skills, _ := cursor.ReadSkills(root)
	for _, sk := range skills {
		out = append(out, rule.Rule{
			Name:     sk.Name,
			Kind:     "skill",
			Source:   "local",
			Adapter:  "cursor",
			Content:  sk.Body,
			Scope:    "project",
			Priority: 50,
			Triggers: []rule.Trigger{{Type: rule.TriggerAlways}},
		})
	}
	return out, nil
}
