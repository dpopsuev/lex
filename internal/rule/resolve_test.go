package rule

import (
	"strings"
	"testing"
)

func TestScoreRule_Always(t *testing.T) {
	r := Rule{Name: "a", Priority: 50, Triggers: []Trigger{{Type: TriggerAlways}}}
	s := ScoreRule(r, ContextSignals{})
	if s <= 0 {
		t.Fatalf("always trigger should produce positive score, got %f", s)
	}
}

func TestScoreRule_FileGlob(t *testing.T) {
	r := Rule{Name: "go", Priority: 50, Triggers: []Trigger{
		{Type: TriggerFileGlob, Pattern: "*.go"},
	}}

	s1 := ScoreRule(r, ContextSignals{Files: []string{"main.go"}})
	if s1 <= 0 {
		t.Fatalf("glob should match main.go, got %f", s1)
	}

	s2 := ScoreRule(r, ContextSignals{Files: []string{"main.py"}})
	if s2 != 0 {
		t.Fatalf("glob should not match main.py, got %f", s2)
	}
}

func TestScoreRule_Language(t *testing.T) {
	r := Rule{Name: "go", Priority: 50, Triggers: []Trigger{
		{Type: TriggerLanguage, Pattern: "go"},
	}}

	s := ScoreRule(r, ContextSignals{Language: "Go"})
	if s <= 0 {
		t.Fatalf("language should match case-insensitively, got %f", s)
	}

	s2 := ScoreRule(r, ContextSignals{Language: "python"})
	if s2 != 0 {
		t.Fatalf("language should not match python, got %f", s2)
	}
}

func TestScoreRule_Keyword(t *testing.T) {
	r := Rule{Name: "sec", Priority: 50, Triggers: []Trigger{
		{Type: TriggerKeyword, Pattern: "security"},
	}}

	s := ScoreRule(r, ContextSignals{Keywords: []string{"Security"}})
	if s <= 0 {
		t.Fatalf("keyword should match case-insensitively, got %f", s)
	}
}

func TestScoreRule_NoTriggers_PassThrough(t *testing.T) {
	r := Rule{Name: "generic", Priority: 50}
	s := ScoreRule(r, ContextSignals{Language: "go"})
	if s <= 0 {
		t.Fatalf("no triggers should pass through with positive score, got %f", s)
	}
}

func TestScoreRule_HasTriggers_NoMatch(t *testing.T) {
	r := Rule{Name: "py", Priority: 50, Triggers: []Trigger{
		{Type: TriggerLanguage, Pattern: "python"},
	}}
	s := ScoreRule(r, ContextSignals{Language: "go"})
	if s != 0 {
		t.Fatalf("unmatched trigger should score 0, got %f", s)
	}
}

func TestScoreRule_PriorityWeight(t *testing.T) {
	hi := Rule{Name: "hi", Priority: 100, Triggers: []Trigger{{Type: TriggerAlways}}}
	lo := Rule{Name: "lo", Priority: 10, Triggers: []Trigger{{Type: TriggerAlways}}}

	sHi := ScoreRule(hi, ContextSignals{})
	sLo := ScoreRule(lo, ContextSignals{})
	if sHi <= sLo {
		t.Fatalf("higher priority should score higher: hi=%f lo=%f", sHi, sLo)
	}
}

func TestResolve_RanksByScore(t *testing.T) {
	rules := []Rule{
		{Name: "generic", Priority: 50, Content: "generic rule"},
		{Name: "go-specific", Priority: 50, Content: "go rule", Triggers: []Trigger{
			{Type: TriggerLanguage, Pattern: "go"},
		}},
		{Name: "always", Priority: 50, Content: "always rule", Triggers: []Trigger{
			{Type: TriggerAlways},
		}},
	}

	result := Resolve(rules, ResolveOpts{
		Signals: ContextSignals{Language: "go"},
	})

	if len(result) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(result))
	}
	// go-specific should rank first (language match = 5.0 * 0.5 = 2.5)
	if result[0].Name != "go-specific" {
		t.Fatalf("expected go-specific first, got %s", result[0].Name)
	}
}

func TestResolve_Deduplication(t *testing.T) {
	rules := []Rule{
		{Name: "dup", Priority: 30, Content: "low priority"},
		{Name: "dup", Priority: 80, Content: "high priority"},
	}

	result := Resolve(rules, ResolveOpts{})
	if len(result) != 1 {
		t.Fatalf("expected 1 rule after dedup, got %d", len(result))
	}
	if result[0].Priority != 80 {
		t.Fatalf("expected high priority winner, got priority %d", result[0].Priority)
	}
}

func TestResolve_TokenBudget(t *testing.T) {
	rules := []Rule{
		{Name: "a", Priority: 50, Content: strings.Repeat("x", 400)}, // ~100 tokens
		{Name: "b", Priority: 50, Content: strings.Repeat("x", 400)}, // ~100 tokens
		{Name: "c", Priority: 50, Content: strings.Repeat("x", 400)}, // ~100 tokens
	}

	result := Resolve(rules, ResolveOpts{Budget: 200})
	if len(result) != 2 {
		t.Fatalf("expected 2 rules within budget, got %d", len(result))
	}
}

func TestResolve_BudgetZero_Unlimited(t *testing.T) {
	rules := []Rule{
		{Name: "a", Priority: 50, Content: strings.Repeat("x", 4000)},
		{Name: "b", Priority: 50, Content: strings.Repeat("x", 4000)},
	}

	result := Resolve(rules, ResolveOpts{Budget: 0})
	if len(result) != 2 {
		t.Fatalf("budget 0 should be unlimited, got %d rules", len(result))
	}
}

func TestResolve_NoSignals_KeepsAll(t *testing.T) {
	rules := []Rule{
		{Name: "a", Priority: 50, Content: "a", Triggers: []Trigger{
			{Type: TriggerLanguage, Pattern: "go"},
		}},
		{Name: "b", Priority: 50, Content: "b", Triggers: []Trigger{
			{Type: TriggerLanguage, Pattern: "python"},
		}},
	}

	result := Resolve(rules, ResolveOpts{})
	if len(result) != 2 {
		t.Fatalf("no signals should keep all rules, got %d", len(result))
	}
}

func TestResolve_FiltersZeroScore(t *testing.T) {
	rules := []Rule{
		{Name: "go", Priority: 50, Content: "go", Triggers: []Trigger{
			{Type: TriggerLanguage, Pattern: "go"},
		}},
		{Name: "py", Priority: 50, Content: "py", Triggers: []Trigger{
			{Type: TriggerLanguage, Pattern: "python"},
		}},
	}

	result := Resolve(rules, ResolveOpts{
		Signals: ContextSignals{Language: "go"},
	})
	if len(result) != 1 {
		t.Fatalf("expected 1 rule (python filtered out), got %d", len(result))
	}
	if result[0].Name != "go" {
		t.Fatalf("expected go rule, got %s", result[0].Name)
	}
}

func TestMatchGlob_DoubleStarPrefix(t *testing.T) {
	if !matchGlob("src/internal/store.go", "**/*.go") {
		t.Fatal("**/*.go should match src/internal/store.go")
	}
	if matchGlob("src/internal/store.py", "**/*.go") {
		t.Fatal("**/*.go should not match store.py")
	}
}

func TestTokenEstimate(t *testing.T) {
	r := Rule{Content: strings.Repeat("x", 100)}
	if r.TokenEstimate() != 25 {
		t.Fatalf("expected 25, got %d", r.TokenEstimate())
	}

	empty := Rule{Content: ""}
	if empty.TokenEstimate() != 0 {
		t.Fatalf("expected 0, got %d", empty.TokenEstimate())
	}

	tiny := Rule{Content: "hi"}
	if tiny.TokenEstimate() != 1 {
		t.Fatalf("expected 1 for small content, got %d", tiny.TokenEstimate())
	}
}
