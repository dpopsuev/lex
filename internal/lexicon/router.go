package lexicon

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dpopsuev/lex/internal/adapter"
	"github.com/dpopsuev/lex/internal/frontmatter"
	"github.com/dpopsuev/lex/internal/registry"
	"github.com/dpopsuev/lex/internal/rule"
)

type ResolvedRule struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	Priority    int      `json:"priority"`
	Body        string   `json:"body"`
	Labels      []string `json:"labels,omitempty"`
	Globs       []string `json:"globs,omitempty"`
	AlwaysApply bool     `json:"always_apply,omitempty"`
}

type ResolvedSkill struct {
	Name     string   `json:"name"`
	Source   string   `json:"source"`
	Priority int      `json:"priority"`
	Body     string   `json:"body"`
	Labels   []string `json:"labels,omitempty"`
}

type Resolution struct {
	Rules  []ResolvedRule  `json:"rules"`
	Skills []ResolvedSkill `json:"skills"`
}

// ResolveOpts controls filtering beyond basic path/label filters.
type ResolveOpts struct {
	PathFilter string
	Labels     []string

	// ActiveFile is the file currently being edited.
	// When set, only rules whose routing globs match or that are always_apply
	// are returned. Rules with no routing data pass through.
	ActiveFile string

	// Context is a set of hot keywords from the workspace (open files, domain
	// terms, project names). Matched against artifact labels.
	Context []string

	// Signals provides context for the scoring engine (v0.5.0+).
	// When set, rules are scored and ranked by relevance.
	Signals rule.ContextSignals

	// Budget is the maximum token count for returned rules (0 = unlimited).
	Budget int
}

func Resolve(_ context.Context, reg *registry.Registry, workspaceRoot string, opts ResolveOpts) (*Resolution, error) {
	res := &Resolution{}

	// Load local rules via adapter registry (detects .cursor/, CLAUDE.md, AGENTS.md, etc.)
	localRules, _ := adapter.DetectAndLoad(workspaceRoot)
	for _, r := range localRules {
		switch r.Kind {
		case "skill":
			res.Skills = append(res.Skills, ResolvedSkill{
				Name:     r.Name,
				Source:   r.Source,
				Priority: r.Priority,
				Body:     r.Content,
				Labels:   r.Labels,
			})
		default:
			res.Rules = append(res.Rules, ResolvedRule{
				Name:        r.Name,
				Source:      r.Source,
				Priority:    r.Priority,
				Body:        r.Content,
				Labels:      r.Labels,
				Globs:       r.Globs,
				AlwaysApply: hasTrigger(r.Triggers, rule.TriggerAlways),
			})
		}
	}

	// Load remote sources from registry.
	sources, _ := reg.Load()
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		cfg, _ := registry.LoadLexiconConfig(src.LocalPath)

		effectivePriority := src.Priority
		if cfg != nil && cfg.Defaults.Priority > 0 {
			effectivePriority = cfg.Defaults.Priority
		}

		artifacts := registry.DiscoverArtifacts(src.LocalPath, src.URL, effectivePriority)
		for _, a := range artifacts {
			if opts.PathFilter != "" && !matchesPath(a, opts.PathFilter) {
				continue
			}
			if len(opts.Labels) > 0 && !matchesLabels(a, opts.Labels) {
				continue
			}

			_, body := readArtifactBody(a.Path)
			globs, alwaysApply := applyRouting(cfg, a.Labels)

			switch a.Type {
			case "rule", "template":
				res.Rules = append(res.Rules, ResolvedRule{
					Name:        a.Name,
					Source:      a.Source,
					Priority:    a.Priority,
					Body:        body,
					Labels:      a.Labels,
					Globs:       globs,
					AlwaysApply: alwaysApply,
				})
			case "skill":
				res.Skills = append(res.Skills, ResolvedSkill{
					Name:     a.Name,
					Source:   a.Source,
					Priority: a.Priority,
					Body:     body,
					Labels:   a.Labels,
				})
			}
		}
	}

	// Legacy smart routing (ActiveFile/Context filtering).
	smartRouting := opts.ActiveFile != "" || len(opts.Context) > 0
	if smartRouting {
		res.Rules = filterRulesByContext(res.Rules, opts.ActiveFile, opts.Context)
		res.Skills = filterSkillsByContext(res.Skills, opts.Context)
	}

	res.Rules = deduplicateRules(res.Rules)
	res.Skills = deduplicateSkills(res.Skills)

	// Apply new context-aware scoring + token budget if signals provided.
	hasSignals := opts.Signals.Language != "" ||
		len(opts.Signals.Files) > 0 ||
		len(opts.Signals.Keywords) > 0 ||
		opts.Budget > 0
	if hasSignals {
		res.Rules = scoreAndBudgetRules(res.Rules, opts.Signals, opts.Budget)
	}

	return res, nil
}

func hasTrigger(triggers []rule.Trigger, t rule.TriggerType) bool {
	for _, tr := range triggers {
		if tr.Type == t {
			return true
		}
	}
	return false
}

// scoreAndBudgetRules converts resolved rules to rule.Rule, runs the scoring
// engine, and converts back.
func scoreAndBudgetRules(rules []ResolvedRule, signals rule.ContextSignals, budget int) []ResolvedRule {
	rr := make([]rule.Rule, len(rules))
	for i, r := range rules {
		rr[i] = rule.Rule{
			Name:     r.Name,
			Kind:     "rule",
			Source:   r.Source,
			Content:  r.Body,
			Priority: r.Priority,
			Labels:   r.Labels,
			Globs:    r.Globs,
		}
		if r.AlwaysApply {
			rr[i].Triggers = append(rr[i].Triggers, rule.Trigger{Type: rule.TriggerAlways})
		}
		for _, g := range r.Globs {
			rr[i].Triggers = append(rr[i].Triggers, rule.Trigger{Type: rule.TriggerFileGlob, Pattern: g})
		}
		for _, l := range r.Labels {
			rr[i].Triggers = append(rr[i].Triggers, rule.Trigger{Type: rule.TriggerKeyword, Pattern: l})
		}
	}

	scored := rule.Resolve(rr, rule.ResolveOpts{Signals: signals, Budget: budget})

	out := make([]ResolvedRule, len(scored))
	for i, r := range scored {
		out[i] = ResolvedRule{
			Name:     r.Name,
			Source:   r.Source,
			Priority: r.Priority,
			Body:     r.Content,
			Labels:   r.Labels,
			Globs:    r.Globs,
		}
		out[i].AlwaysApply = hasTrigger(r.Triggers, rule.TriggerAlways)
	}
	return out
}

// filterRulesByContext keeps rules that are contextually relevant:
//   - always_apply rules pass unconditionally
//   - rules whose globs match activeFile pass
//   - rules whose labels intersect context keywords pass
//   - rules with no routing metadata (no globs, no always_apply, no labels) pass
func filterRulesByContext(rules []ResolvedRule, activeFile string, ctxKeywords []string) []ResolvedRule {
	ctxSet := toLowerSet(ctxKeywords)
	var out []ResolvedRule
	for _, r := range rules {
		if r.AlwaysApply {
			out = append(out, r)
			continue
		}
		hasRouting := len(r.Globs) > 0 || len(r.Labels) > 0
		if !hasRouting {
			out = append(out, r)
			continue
		}
		if activeFile != "" && matchesAnyGlob(activeFile, r.Globs) {
			out = append(out, r)
			continue
		}
		if len(ctxSet) > 0 && labelsIntersect(r.Labels, ctxSet) {
			out = append(out, r)
			continue
		}
	}
	return out
}

func filterSkillsByContext(skills []ResolvedSkill, ctxKeywords []string) []ResolvedSkill {
	if len(ctxKeywords) == 0 {
		return skills
	}
	ctxSet := toLowerSet(ctxKeywords)
	var out []ResolvedSkill
	for _, s := range skills {
		if len(s.Labels) == 0 || labelsIntersect(s.Labels, ctxSet) {
			out = append(out, s)
		}
	}
	return out
}

// matchesAnyGlob checks whether filePath matches any of the provided glob patterns.
func matchesAnyGlob(filePath string, globs []string) bool {
	if filePath == "" || len(globs) == 0 {
		return false
	}
	base := filepath.Base(filePath)
	for _, g := range globs {
		if matched, _ := filepath.Match(g, filePath); matched {
			return true
		}
		if matched, _ := filepath.Match(g, base); matched {
			return true
		}
		// Handle **/ prefix: strip it and match against basename or tail
		if strings.HasPrefix(g, "**/") {
			tail := g[3:]
			if matched, _ := filepath.Match(tail, base); matched {
				return true
			}
			// Also try matching against each suffix of the path
			parts := strings.Split(filepath.ToSlash(filePath), "/")
			for i := range parts {
				candidate := strings.Join(parts[i:], "/")
				if matched, _ := filepath.Match(tail, candidate); matched {
					return true
				}
			}
		}
	}
	return false
}

func labelsIntersect(labels []string, ctxSet map[string]bool) bool {
	for _, l := range labels {
		if ctxSet[strings.ToLower(l)] {
			return true
		}
	}
	return false
}

func toLowerSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[strings.ToLower(item)] = true
	}
	return s
}

// applyRouting checks routing rules from lexicon.yaml against artifact labels
// and returns merged globs and always_apply flag.
func applyRouting(cfg *registry.LexiconConfig, artifactLabels []string) ([]string, bool) {
	if cfg == nil || len(cfg.Routing) == 0 {
		return nil, false
	}

	labelSet := make(map[string]bool, len(artifactLabels))
	for _, l := range artifactLabels {
		labelSet[strings.ToLower(l)] = true
	}

	var globs []string
	alwaysApply := false

	for _, rule := range cfg.Routing {
		if !routeMatches(rule.Match, labelSet) {
			continue
		}
		globs = append(globs, rule.Globs...)
		if rule.Always {
			alwaysApply = true
		}
	}
	return globs, alwaysApply
}

func routeMatches(m registry.MatchCriteria, labelSet map[string]bool) bool {
	if len(m.Labels) == 0 {
		return false
	}
	for _, l := range m.Labels {
		if labelSet[strings.ToLower(l)] {
			return true
		}
	}
	return false
}

func deduplicateRules(rules []ResolvedRule) []ResolvedRule {
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})
	seen := make(map[string]bool)
	var out []ResolvedRule
	for _, r := range rules {
		if seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		out = append(out, r)
	}
	return out
}

func deduplicateSkills(skills []ResolvedSkill) []ResolvedSkill {
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Priority > skills[j].Priority
	})
	seen := make(map[string]bool)
	var out []ResolvedSkill
	for _, s := range skills {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		out = append(out, s)
	}
	return out
}

func matchesPath(a registry.Artifact, filter string) bool {
	return strings.Contains(a.Path, filter) || strings.Contains(a.Name, filter)
}

// matchesLabels checks if any of the requested labels appear in the artifact's
// actual frontmatter labels, falling back to name/type substring for artifacts
// without label metadata.
func matchesLabels(a registry.Artifact, labels []string) bool {
	if len(a.Labels) > 0 {
		aSet := make(map[string]bool, len(a.Labels))
		for _, l := range a.Labels {
			aSet[strings.ToLower(l)] = true
		}
		for _, l := range labels {
			if aSet[strings.ToLower(l)] {
				return true
			}
		}
		return false
	}
	nameLC := strings.ToLower(a.Name)
	typeLC := strings.ToLower(a.Type)
	for _, l := range labels {
		l = strings.ToLower(l)
		if strings.Contains(nameLC, l) || typeLC == l {
			return true
		}
	}
	return false
}

// readArtifactBody reads a file and strips frontmatter, returning the body.
func readArtifactBody(path string) (frontmatter.Meta, string) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	fm, body := frontmatter.Parse(string(data))
	return fm, strings.TrimSpace(body)
}
