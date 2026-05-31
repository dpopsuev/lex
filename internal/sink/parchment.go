// Package sink writes Lex rules and skills into a Parchment store.
// Optional dependency — if SCRIBE_ROOT is not set the sink is never opened.
package sink

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"

	parchment "github.com/dpopsuev/parchment"
	"github.com/dpopsuev/ordo/lexicon"
)

// ParchmentSink writes rule and skill artifacts to a Parchment SQLite store.
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

// SyncResolution upserts all rules and skills from a Resolution into Parchment.
// Ensures kind=rule and kind=skill definitions exist first.
// Reconciles stale artifacts. Best-effort — failures are logged, not fatal.
func (s *ParchmentSink) SyncResolution(ctx context.Context, res *lexicon.Resolution) {
	if res == nil {
		return
	}
	_ = s.ensureKindDefinition(ctx, "rule")
	_ = s.ensureKindDefinition(ctx, "skill")

	currentIDs := make(map[string]bool, len(res.Rules)+len(res.Skills))

	for _, r := range res.Rules {
		art := resolvedRuleToArtifact(r)
		currentIDs[art.ID] = true
		if err := s.store.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "sink: put rule failed",
				slog.String("id", art.ID), slog.Any("error", err))
		}
	}
	for _, sk := range res.Skills {
		art := resolvedSkillToArtifact(sk)
		currentIDs[art.ID] = true
		if err := s.store.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "sink: put skill failed",
				slog.String("id", art.ID), slog.Any("error", err))
		}
	}

	// Reconcile: remove stale rule/skill artifacts.
	for _, scope := range []string{"global", "project"} {
		existing, err := s.store.List(ctx, parchment.Filter{Scope: scope})
		if err != nil {
			continue
		}
		for _, art := range existing {
			if art.Kind != "rule" && art.Kind != "skill" {
				continue
			}
			if !currentIDs[art.ID] {
				_ = s.store.Delete(ctx, art.ID)
			}
		}
	}
}

func resolvedRuleToArtifact(r lexicon.ResolvedRule) *parchment.Artifact {
	labels := make([]string, len(r.Labels))
	copy(labels, r.Labels)
	if r.Source != "" {
		labels = append(labels, "source:"+r.Source)
	}
	return &parchment.Artifact{
		ID:     artifactID("rule", r.Source, r.Name),
		Kind:   "rule",
		Scope:  "global",
		Title:  r.Name,
		Status: parchment.StatusActive,
		Labels: labels,
		Extra: map[string]any{
			"priority":     r.Priority,
			"source":       r.Source,
			"always_apply": r.AlwaysApply,
		},
		Sections: []parchment.Section{
			{Name: "content", Text: r.Body},
		},
	}
}

func resolvedSkillToArtifact(sk lexicon.ResolvedSkill) *parchment.Artifact {
	labels := make([]string, len(sk.Labels))
	copy(labels, sk.Labels)
	if sk.Source != "" {
		labels = append(labels, "source:"+sk.Source)
	}
	return &parchment.Artifact{
		ID:     artifactID("skill", sk.Source, sk.Name),
		Kind:   "skill",
		Scope:  "global",
		Title:  sk.Name,
		Status: parchment.StatusActive,
		Labels: labels,
		Extra: map[string]any{
			"priority": sk.Priority,
			"source":   sk.Source,
		},
		Sections: []parchment.Section{
			{Name: "content", Text: sk.Body},
		},
	}
}

// artifactID returns a deterministic ID: LDEF-<8-char hash>.
func artifactID(kind, source, name string) string {
	key := kind + ":" + source + "/" + name
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("LDEF-%x", h[:4])
}

// ensureKindDefinition creates a kind=definition artifact in _schema scope
// for the given kind if one does not already exist.
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
