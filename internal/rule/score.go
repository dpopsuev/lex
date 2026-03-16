package rule

import (
	"path/filepath"
	"strings"
)

// ScoreRule returns a relevance score for the rule given the context signals.
// Higher scores indicate stronger relevance. Returns 0 if the rule is
// explicitly excluded (has triggers but none match).
func ScoreRule(r Rule, signals ContextSignals) float64 {
	if len(r.Triggers) == 0 {
		// No triggers = untagged rule, pass through with baseline score.
		return 1.0 * priorityWeight(r.Priority)
	}

	var score float64
	matched := false

	for _, t := range r.Triggers {
		switch t.Type {
		case TriggerAlways:
			score += 1.0
			matched = true

		case TriggerFileGlob:
			for _, f := range signals.Files {
				if matchGlob(f, t.Pattern) {
					score += 10.0
					matched = true
					break
				}
			}

		case TriggerLanguage:
			if signals.Language != "" && strings.EqualFold(t.Pattern, signals.Language) {
				score += 5.0
				matched = true
			}

		case TriggerKeyword:
			for _, kw := range signals.Keywords {
				if strings.EqualFold(t.Pattern, kw) {
					score += 3.0
					matched = true
					break
				}
			}
		}
	}

	if !matched {
		return 0
	}
	return score * priorityWeight(r.Priority)
}

func priorityWeight(priority int) float64 {
	if priority <= 0 {
		return 0.01
	}
	return float64(priority) / 100.0
}

// matchGlob checks whether filePath matches a glob pattern, supporting
// the **/ prefix convention.
func matchGlob(filePath, pattern string) bool {
	if filePath == "" || pattern == "" {
		return false
	}
	base := filepath.Base(filePath)

	if matched, _ := filepath.Match(pattern, filePath); matched {
		return true
	}
	if matched, _ := filepath.Match(pattern, base); matched {
		return true
	}
	if strings.HasPrefix(pattern, "**/") {
		tail := pattern[3:]
		if matched, _ := filepath.Match(tail, base); matched {
			return true
		}
		parts := strings.Split(filepath.ToSlash(filePath), "/")
		for i := range parts {
			candidate := strings.Join(parts[i:], "/")
			if matched, _ := filepath.Match(tail, candidate); matched {
				return true
			}
		}
	}
	return false
}
