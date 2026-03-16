package lexicon

import (
	"context"
	"strings"

	"github.com/dpopsuev/lex/internal/registry"
)

// Match represents a search hit within a resolved lexicon artifact.
type Match struct {
	Type    string   `json:"type"`    // "rule" or "skill"
	Name    string   `json:"name"`
	Snippet string   `json:"snippet"`
	Source  string   `json:"source"`
	File    string   `json:"file,omitempty"`
	Priority int     `json:"priority"`
	Labels  []string `json:"labels,omitempty"`
}

// Search finds leges (rules and skills) matching a substring query across
// loaded lexicons. If sources is empty, all sources are searched.
func Search(ctx context.Context, reg *registry.Registry, workspaceRoot string, query string, sources []string) ([]Match, error) {
	if query == "" {
		return nil, nil
	}

	q := strings.ToLower(query)
	sourceSet := toSourceSet(sources)

	// Resolve all rules/skills (unfiltered).
	res, err := Resolve(ctx, reg, workspaceRoot, ResolveOpts{})
	if err != nil {
		return nil, err
	}

	var matches []Match

	for _, r := range res.Rules {
		if len(sourceSet) > 0 && !sourceSet[r.Source] {
			continue
		}
		if idx := indexFold(r.Body, q); idx >= 0 {
			matches = append(matches, Match{
				Type:     "rule",
				Name:     r.Name,
				Snippet:  extractSnippet(r.Body, idx, len(query), 50),
				Source:   r.Source,
				Priority: r.Priority,
				Labels:   r.Labels,
			})
		} else if indexFold(r.Name, q) >= 0 {
			matches = append(matches, Match{
				Type:     "rule",
				Name:     r.Name,
				Snippet:  truncate(r.Body, 100),
				Source:   r.Source,
				Priority: r.Priority,
				Labels:   r.Labels,
			})
		}
	}

	for _, s := range res.Skills {
		if len(sourceSet) > 0 && !sourceSet[s.Source] {
			continue
		}
		if idx := indexFold(s.Body, q); idx >= 0 {
			matches = append(matches, Match{
				Type:     "skill",
				Name:     s.Name,
				Snippet:  extractSnippet(s.Body, idx, len(query), 50),
				Source:   s.Source,
				Priority: s.Priority,
				Labels:   s.Labels,
			})
		} else if indexFold(s.Name, q) >= 0 {
			matches = append(matches, Match{
				Type:     "skill",
				Name:     s.Name,
				Snippet:  truncate(s.Body, 100),
				Source:   s.Source,
				Priority: s.Priority,
				Labels:   s.Labels,
			})
		}
	}

	return matches, nil
}

// indexFold returns the index of the first case-insensitive occurrence of
// substr in s, or -1 if not found.
func indexFold(s, substr string) int {
	return strings.Index(strings.ToLower(s), substr)
}

// extractSnippet returns a substring of body centered on the match position,
// with context chars on each side.
func extractSnippet(body string, matchIdx, matchLen, context int) string {
	start := matchIdx - context
	if start < 0 {
		start = 0
	}
	end := matchIdx + matchLen + context
	if end > len(body) {
		end = len(body)
	}

	snippet := body[start:end]
	// Clean up whitespace.
	snippet = strings.Join(strings.Fields(snippet), " ")

	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "..."
	}
	if end < len(body) {
		suffix = "..."
	}
	return prefix + snippet + suffix
}

func truncate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func toSourceSet(sources []string) map[string]bool {
	if len(sources) == 0 {
		return nil
	}
	m := make(map[string]bool, len(sources))
	for _, s := range sources {
		m[s] = true
	}
	return m
}
