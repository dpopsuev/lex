// Package sink writes Lex rules and skills into a Parchment store.
// Optional dependency — if SCRIBE_ROOT is not set the sink is never opened.
package sink

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	parchment "github.com/dpopsuev/parchment"
	"github.com/dpopsuev/ordo/adapter"
	"github.com/dpopsuev/ordo/lexicon"
	"github.com/dpopsuev/ordo/registry"
	"github.com/dpopsuev/ordo/rule"
)

// ParchmentSink writes rule and skill artifacts to a Parchment SQLite store
// and reads them back for resolution queries.
type ParchmentSink struct {
	store parchment.Store
}

// Open opens the Parchment store at scribeRoot/scribe.sqlite.
func Open(scribeRoot string) (*ParchmentSink, error) {
	if scribeRoot == "" {
		return nil, fmt.Errorf("SCRIBE_ROOT is empty") //nolint:err113
	}
	store, err := parchment.OpenSQLite(scribeRoot + "/scribe.sqlite")
	if err != nil {
		return nil, fmt.Errorf("open parchment store: %w", err)
	}
	return &ParchmentSink{store: store}, nil
}

// Close closes the underlying store.
func (s *ParchmentSink) Close() error {
	return s.store.Close()
}

const kindRule = "rule"
const kindSkill = "skill"

// SyncAll loads ALL rules and skills from every registered source and
// local adapters, then bulk-upserts them into Parchment.
// This is the preferred startup path — it captures everything, not just
// the context-filtered subset that Resolve would return for a specific CWD.
func (s *ParchmentSink) SyncAll(ctx context.Context, reg *registry.Registry, cwd string) { //nolint:funlen // sequential: load local + all remote + bulk-put + reconcile
	_ = s.ensureKindDefinition(ctx, kindRule)
	_ = s.ensureKindDefinition(ctx, kindSkill)

	var arts []*parchment.Artifact
	currentIDs := make(map[string]bool)

	// Local adapters (cursor, claude, codex, etc.)
	if cwd != "" {
		localRules, _ := adapter.DetectAndLoad(cwd)
		for _, r := range localRules {
			art := ruleRuleToArtifact(r)
			currentIDs[art.ID] = true
			arts = append(arts, art)
		}
	}

	// All registered remote sources — discover every artifact, no label filtering.
	sources, _ := reg.Load()
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		cfg, _ := registry.LoadLexiconConfig(src.LocalPath)
		priority := src.Priority
		if cfg != nil && cfg.Defaults.Priority > 0 {
			priority = cfg.Defaults.Priority
		}

		artifacts := registry.DiscoverArtifacts(src.LocalPath, src.URL, priority)
		for _, a := range artifacts {
			body := readBody(a.Path)
			art := discoveredArtifactToArtifact(a, body)
			currentIDs[art.ID] = true
			arts = append(arts, art)
		}
	}

	// BulkPut — single transaction for all artifacts.
	if len(arts) > 0 {
		errs := s.store.BulkPut(ctx, arts)
		for i, e := range errs {
			if e != nil && i < len(arts) {
				slog.WarnContext(ctx, "sink: bulk put failed",
					slog.String("id", arts[i].ID), slog.Any("error", e))
			}
		}
	}

	// Reconcile: remove stale rule/skill artifacts.
	for _, scope := range []string{"global", "project"} {
		existing, err := s.store.List(ctx, parchment.Filter{Scope: scope})
		if err != nil {
			continue
		}
		for _, art := range existing {
			if art.Kind != kindRule && art.Kind != kindSkill {
				continue
			}
			if !currentIDs[art.ID] {
				_ = s.store.Delete(ctx, art.ID)
			}
		}
	}

	slog.InfoContext(ctx, "sink: synced all rules",
		slog.Int("count", len(arts)))
}

// SyncResolution upserts rules from a pre-filtered Resolution using BulkPut.
// Use SyncAll when you want all rules; use SyncResolution when you have a
// pre-resolved set (e.g. from a specific context).
func (s *ParchmentSink) SyncResolution(ctx context.Context, res *lexicon.Resolution) {
	if res == nil {
		return
	}
	_ = s.ensureKindDefinition(ctx, kindRule)
	_ = s.ensureKindDefinition(ctx, kindSkill)

	arts := make([]*parchment.Artifact, 0, len(res.Rules)+len(res.Skills))
	currentIDs := make(map[string]bool, len(res.Rules)+len(res.Skills))

	for _, r := range res.Rules {
		art := resolvedRuleToArtifact(r)
		currentIDs[art.ID] = true
		arts = append(arts, art)
	}
	for _, sk := range res.Skills {
		art := resolvedSkillToArtifact(sk)
		currentIDs[art.ID] = true
		arts = append(arts, art)
	}

	if len(arts) > 0 {
		errs := s.store.BulkPut(ctx, arts)
		for i, e := range errs {
			if e != nil && i < len(arts) {
				slog.WarnContext(ctx, "sink: bulk put failed",
					slog.String("id", arts[i].ID), slog.Any("error", e))
			}
		}
	}

	// Reconcile stale artifacts.
	for _, scope := range []string{"global", "project"} {
		existing, _ := s.store.List(ctx, parchment.Filter{Scope: scope})
		for _, art := range existing {
			if art.Kind != kindRule && art.Kind != kindSkill {
				continue
			}
			if !currentIDs[art.ID] {
				_ = s.store.Delete(ctx, art.ID)
			}
		}
	}
}

// ResolveFromParchment queries Parchment for rules and skills matching labels.
// Returns a lexicon.Resolution suitable for use by the resolve action.
// Returns nil if no results are found (caller should fall back to Ordo).
func (s *ParchmentSink) ResolveFromParchment(ctx context.Context, labels []string) *lexicon.Resolution {
	filter := parchment.Filter{}
	if len(labels) > 0 {
		filter.Labels = labels
	}

	all, err := s.store.List(ctx, filter)
	if err != nil || len(all) == 0 {
		return nil
	}

	res := &lexicon.Resolution{}
	for _, art := range all {
		if art.Kind != kindRule && art.Kind != kindSkill {
			continue
		}
		body := ""
		for _, sec := range art.Sections {
			if sec.Name == "content" {
				body = sec.Text
				break
			}
		}
		priority, _ := art.Extra["priority"].(float64)
		alwaysApply, _ := art.Extra["always_apply"].(bool)
		source, _ := art.Extra["source"].(string)

		if art.Kind == kindSkill {
			res.Skills = append(res.Skills, lexicon.ResolvedSkill{
				Name:     art.Title,
				Source:   source,
				Priority: int(priority),
				Body:     body,
				Labels:   art.Labels,
			})
		} else {
			res.Rules = append(res.Rules, lexicon.ResolvedRule{
				Name:        art.Title,
				Source:      source,
				Priority:    int(priority),
				Body:        body,
				Labels:      art.Labels,
				AlwaysApply: alwaysApply,
			})
		}
	}

	if len(res.Rules) == 0 && len(res.Skills) == 0 {
		return nil
	}
	return res
}

// --- conversion helpers ---

// ruleRuleToArtifact converts a rule.Rule (from local adapters) to a Parchment artifact.
func ruleRuleToArtifact(r rule.Rule) *parchment.Artifact {
	kind := kindRule
	if strings.ToLower(r.Kind) == kindSkill {
		kind = kindSkill
	}
	alwaysApply := false
	for _, t := range r.Triggers {
		if t.Type == rule.TriggerAlways {
			alwaysApply = true
			break
		}
	}
	labels := appendSourceLabel(r.Labels, r.Source)
	now := time.Now().UTC()
	return &parchment.Artifact{
		ID:         artifactID(kind, r.Source, r.Name),
		Kind:       kind,
		Scope:      "global",
		Title:      r.Name,
		Status:     parchment.StatusActive,
		Labels:     labels,
		CreatedAt:  now,
		UpdatedAt:  now,
		InsertedAt: now,
		Extra: map[string]any{
			"priority":     r.Priority,
			"source":       r.Source,
			"adapter":      r.Adapter,
			"always_apply": alwaysApply,
		},
		Sections: []parchment.Section{{Name: "content", Text: r.Content}},
	}
}

// readBody reads file content, stripping YAML frontmatter if present.
func readBody(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // operator-controlled path from Ordo registry
	if err != nil {
		return ""
	}
	content := string(data)
	// Strip frontmatter block if present.
	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---\n")
		if end >= 0 {
			content = strings.TrimSpace(content[4+end+5:])
		}
	}
	return content
}

func resolvedRuleToArtifact(r lexicon.ResolvedRule) *parchment.Artifact {
	labels := appendSourceLabel(r.Labels, r.Source)
	return &parchment.Artifact{
		ID:     artifactID(kindRule, r.Source, r.Name),
		Kind:   kindRule,
		Scope:  "global",
		Title:  r.Name,
		Status: parchment.StatusActive,
		Labels: labels,
		Extra: map[string]any{
			"priority":     r.Priority,
			"source":       r.Source,
			"always_apply": r.AlwaysApply,
		},
		Sections: []parchment.Section{{Name: "content", Text: r.Body}},
	}
}

func resolvedSkillToArtifact(sk lexicon.ResolvedSkill) *parchment.Artifact {
	labels := appendSourceLabel(sk.Labels, sk.Source)
	return &parchment.Artifact{
		ID:     artifactID(kindSkill, sk.Source, sk.Name),
		Kind:   kindSkill,
		Scope:  "global",
		Title:  sk.Name,
		Status: parchment.StatusActive,
		Labels: labels,
		Extra: map[string]any{
			"priority": sk.Priority,
			"source":   sk.Source,
		},
		Sections: []parchment.Section{{Name: "content", Text: sk.Body}},
	}
}

func discoveredArtifactToArtifact(a registry.Artifact, body string) *parchment.Artifact {
	kind := kindRule
	if a.Type == kindSkill {
		kind = kindSkill
	}
	labels := appendSourceLabel(a.Labels, a.Source)
	now := time.Now().UTC()
	return &parchment.Artifact{
		ID:         artifactID(kind, a.Source, a.Name),
		Kind:       kind,
		Scope:      "global",
		Title:      a.Name,
		Status:     parchment.StatusActive,
		Labels:     labels,
		CreatedAt:  now,
		UpdatedAt:  now,
		InsertedAt: now,
		Extra: map[string]any{
			"priority": a.Priority,
			"source":   a.Source,
		},
		Sections: []parchment.Section{{Name: "content", Text: body}},
	}
}

func appendSourceLabel(labels []string, source string) []string {
	out := make([]string, len(labels))
	copy(out, labels)
	if source != "" {
		out = append(out, "source:"+source)
	}
	return out
}

// artifactID returns a deterministic ID: LDEF-<8-char hash>.
func artifactID(kind, source, name string) string {
	key := kind + ":" + source + "/" + name
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("LDEF-%x", h[:4])
}

// ensureKindDefinition creates a kind=definition artifact in _schema scope.
func (s *ParchmentSink) ensureKindDefinition(ctx context.Context, kindName string) error {
	id := "DEF-" + kindName
	if _, err := s.store.Get(ctx, id); err == nil {
		return nil
	}
	prefix := strings.ToUpper(kindName)
	if len(prefix) > 3 {
		prefix = prefix[:3]
	}
	return s.store.Put(ctx, &parchment.Artifact{
		ID:     id,
		Kind:   parchment.KindDefinition,
		Scope:  parchment.SchemaScope,
		Title:  kindName,
		Status: parchment.StatusActive,
		Extra: map[string]any{
			"prefix":         prefix,
			"code":           prefix,
			"family":         parchment.FamilyKnowledge,
			"default_status": parchment.StatusActive,
			"protected":      true,
			"skip_guards":    true,
		},
	})
}
