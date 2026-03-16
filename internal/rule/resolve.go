package rule

import "sort"

// Resolve scores, filters, deduplicates, and budget-caps rules.
func Resolve(rules []Rule, opts ResolveOpts) []Rule {
	hasSignals := opts.Signals.Language != "" ||
		len(opts.Signals.Files) > 0 ||
		len(opts.Signals.Keywords) > 0

	type scored struct {
		rule  Rule
		score float64
	}

	var candidates []scored
	for _, r := range rules {
		var s float64
		if hasSignals {
			s = ScoreRule(r, opts.Signals)
			if s == 0 {
				continue
			}
		} else {
			// No signals: keep all rules, rank by priority.
			s = priorityWeight(r.Priority)
		}
		candidates = append(candidates, scored{rule: r, score: s})
	}

	// Sort by score descending, priority as tiebreaker.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].rule.Priority > candidates[j].rule.Priority
	})

	// Deduplicate by name (highest score wins).
	seen := make(map[string]bool)
	var deduped []scored
	for _, c := range candidates {
		if seen[c.rule.Name] {
			continue
		}
		seen[c.rule.Name] = true
		deduped = append(deduped, c)
	}

	// Apply token budget.
	var result []Rule
	var totalTokens int
	for _, c := range deduped {
		tokens := c.rule.TokenEstimate()
		if opts.Budget > 0 && totalTokens+tokens > opts.Budget {
			continue
		}
		totalTokens += tokens
		result = append(result, c.rule)
	}
	return result
}
