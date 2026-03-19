package lexicon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/dpopsuev/lex/internal/adapter/cursor"
	"github.com/dpopsuev/lex/internal/registry"
)

func setupTestWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	rulesDir := filepath.Join(dir, ".cursor", "rules")
	os.MkdirAll(rulesDir, 0o755)
	os.WriteFile(filepath.Join(rulesDir, "local-rule.mdc"), []byte("---\ndescription: local rule\n---\nLocal rule body"), 0o644)

	skillsDir := filepath.Join(dir, ".cursor", "skills", "local-skill")
	os.MkdirAll(skillsDir, 0o755)
	os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte("---\nname: local-skill\ndescription: local skill\n---\nLocal skill body"), 0o644)

	return dir
}

func setupTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	return registry.New(t.TempDir())
}

func seedRemoteLexicon(t *testing.T, reg *registry.Registry, name, typ, body string, priority int) string {
	t.Helper()
	url := "https://test.example.com/" + name
	lexDir := reg.LexiconDirForURL(url)
	subdir := typ + "s"

	if typ == "skill" {
		skillDir := filepath.Join(lexDir, subdir, name)
		os.MkdirAll(skillDir, 0o755)
		os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644)
	} else {
		os.MkdirAll(filepath.Join(lexDir, subdir), 0o755)
		os.WriteFile(filepath.Join(lexDir, subdir, name+".md"), []byte(body), 0o644)
	}

	rc := registry.RepoConfig{
		URL:      url,
		Priority: priority,
		Enabled:  true,
	}
	if rc.Priority == 0 {
		rc.Priority = 50
	}
	if err := reg.SaveRepoForTest(&rc); err != nil {
		t.Fatalf("save repo: %v", err)
	}
	return lexDir
}

func TestResolveLocalOnly(t *testing.T) {
	ctx := context.Background()
	reg := setupTestRegistry(t)
	ws := setupTestWorkspace(t)

	res, err := Resolve(ctx, reg, ws, ResolveOpts{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(res.Rules) != 1 || res.Rules[0].Name != "local-rule" {
		t.Fatalf("expected 1 local rule, got %d", len(res.Rules))
	}
	if len(res.Skills) != 1 || res.Skills[0].Name != "local-skill" {
		t.Fatalf("expected 1 local skill, got %d", len(res.Skills))
	}
}

func TestResolvePriorityOverride(t *testing.T) {
	ctx := context.Background()
	reg := setupTestRegistry(t)
	ws := setupTestWorkspace(t)

	seedRemoteLexicon(t, reg, "local-rule", "rule", "Remote override body", 75)

	res, err := Resolve(ctx, reg, ws, ResolveOpts{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var found ResolvedRule
	for _, r := range res.Rules {
		if r.Name == "local-rule" {
			found = r
			break
		}
	}
	if found.Priority != 75 {
		t.Fatalf("expected remote priority 75 to win, got %d from source %q", found.Priority, found.Source)
	}
}

func TestResolvePathFilter(t *testing.T) {
	ctx := context.Background()
	reg := setupTestRegistry(t)
	ws := setupTestWorkspace(t)

	seedRemoteLexicon(t, reg, "go-lint", "rule", "Go lint rules", 30)
	seedRemoteLexicon(t, reg, "py-lint", "rule", "Python lint rules", 30)

	res, err := Resolve(ctx, reg, ws, ResolveOpts{PathFilter: "go"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	remoteCount := 0
	for _, r := range res.Rules {
		if r.Source != "local" {
			remoteCount++
		}
	}
	if remoteCount != 1 {
		t.Fatalf("expected 1 filtered remote rule, got %d", remoteCount)
	}
}

func TestResolveLabelFilter(t *testing.T) {
	ctx := context.Background()
	reg := setupTestRegistry(t)
	ws := setupTestWorkspace(t)

	seedRemoteLexicon(t, reg, "security-check", "rule", "sec", 40)
	seedRemoteLexicon(t, reg, "style-check", "skill", "---\nname: style-check\n---\nstyle", 40)

	res, err := Resolve(ctx, reg, ws, ResolveOpts{Labels: []string{"security"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	remoteRules := 0
	for _, r := range res.Rules {
		if r.Source != "local" {
			remoteRules++
		}
	}
	if remoteRules != 1 {
		t.Fatalf("expected 1 label-matched remote rule, got %d", remoteRules)
	}
}

func TestSmartRoutingByFile(t *testing.T) {
	ctx := context.Background()
	reg := setupTestRegistry(t)
	ws := setupTestWorkspace(t)

	seedRemoteLexicon(t, reg, "test-rule", "rule",
		"---\nid: test-rule\ntitle: Test Rule\nlabels: [testing]\n---\nTest body", 30)
	seedRemoteLexicon(t, reg, "general-rule", "rule",
		"---\nid: general-rule\ntitle: General\nlabels: [style]\n---\nGeneral body", 30)

	// Write a lexicon.yaml with routing globs for the testing label
	lexDir := findLexiconDir(t, reg, "test-rule")
	os.WriteFile(filepath.Join(lexDir, "lexicon.yaml"), []byte(
		"name: test\nversion: \"1.0\"\nrouting:\n  - match: { labels: [testing] }\n    globs: [\"*_test.go\"]\n"), 0o644)

	// With --file pointing at a test file, only test-rule should match
	res, err := Resolve(ctx, reg, ws, ResolveOpts{ActiveFile: "pkg/foo_test.go"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var names []string
	for _, r := range res.Rules {
		if r.Source != "local" {
			names = append(names, r.Name)
		}
	}
	if len(names) != 1 || names[0] != "test-rule" {
		t.Fatalf("expected only test-rule, got %v", names)
	}
}

func TestSmartRoutingByContext(t *testing.T) {
	ctx := context.Background()
	reg := setupTestRegistry(t)
	ws := setupTestWorkspace(t)

	seedRemoteLexicon(t, reg, "ptp-guide", "rule",
		"---\nid: ptp-guide\ntitle: PTP Guide\nlabels: [ptp, networking]\n---\nPTP body", 30)
	seedRemoteLexicon(t, reg, "general-rule", "rule",
		"---\nid: general-rule\ntitle: General\nlabels: [style]\n---\nGeneral body", 30)

	// With context=["ptp"], only ptp-guide should match
	res, err := Resolve(ctx, reg, ws, ResolveOpts{Context: []string{"ptp"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var names []string
	for _, r := range res.Rules {
		if r.Source != "local" {
			names = append(names, r.Name)
		}
	}
	if len(names) != 1 || names[0] != "ptp-guide" {
		t.Fatalf("expected only ptp-guide, got %v", names)
	}
}

func TestSmartRoutingAlwaysApply(t *testing.T) {
	ctx := context.Background()
	reg := setupTestRegistry(t)
	ws := setupTestWorkspace(t)

	seedRemoteLexicon(t, reg, "security-rule", "rule",
		"---\nid: security-rule\ntitle: Security\nlabels: [security]\n---\nSecurity body", 30)
	seedRemoteLexicon(t, reg, "other-rule", "rule",
		"---\nid: other-rule\ntitle: Other\nlabels: [style]\n---\nOther body", 30)

	// Write lexicon.yaml marking security as always_apply
	lexDir := findLexiconDir(t, reg, "security-rule")
	os.WriteFile(filepath.Join(lexDir, "lexicon.yaml"), []byte(
		"name: test\nversion: \"1.0\"\nrouting:\n  - match: { labels: [security] }\n    always_apply: true\n"), 0o644)

	// With an unrelated context, security-rule should still pass (always_apply)
	res, err := Resolve(ctx, reg, ws, ResolveOpts{Context: []string{"unrelated"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	found := false
	for _, r := range res.Rules {
		if r.Name == "security-rule" {
			found = true
			if !r.AlwaysApply {
				t.Fatalf("security-rule should have always_apply=true")
			}
		}
	}
	if !found {
		t.Fatalf("security-rule not found in results")
	}
}

// TestResolveGenericLayout verifies that DiscoverArtifacts finds rules in the
// v0.5.0 generic/ directory layout with a lexicon.yaml routing config.
// This is the real-world layout used by lexicon repos. Regression test for
// LEX-BUG-3 (stale binary missed generic/ path support).
func TestResolveGenericLayout(t *testing.T) {
	ctx := context.Background()
	reg := setupTestRegistry(t)
	ws := setupTestWorkspace(t)

	// Simulate a real lexicon repo with generic/ layout + lexicon.yaml
	url := "https://test.example.com/generic-lexicon"
	lexDir := reg.LexiconDirForURL(url)

	rulesDir := filepath.Join(lexDir, "generic", "rules")
	os.MkdirAll(rulesDir, 0o755)

	os.WriteFile(filepath.Join(rulesDir, "solid-principles.md"), []byte(
		"---\nid: solid-principles\ntitle: SOLID Principles\nlabels: [architecture, design]\n---\n# SOLID Principles\n\nEvery module must satisfy SOLID.",
	), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "code-smells.md"), []byte(
		"---\nid: code-smells\ntitle: Code Smells\nlabels: [architecture, design, refactoring]\n---\n# Code Smells\n\nSmells are symptoms.",
	), 0o644)

	skillsDir := filepath.Join(lexDir, "generic", "skills", "onboard")
	os.MkdirAll(skillsDir, 0o755)
	os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(
		"---\nname: onboard\nlabels: [onboarding]\n---\n# /onboard\n\nTeach newcomers the repo.",
	), 0o644)

	// Write lexicon.yaml with routing (architecture label → always_apply)
	os.WriteFile(filepath.Join(lexDir, "lexicon.yaml"), []byte(
		"name: test-lexicon\nversion: \"1.0\"\ndefaults:\n  priority: 25\nrouting:\n  - match: { labels: [architecture] }\n    always_apply: true\n",
	), 0o644)

	rc := registry.RepoConfig{
		URL:      url,
		Priority: 25,
		Enabled:  true,
	}
	if err := reg.SaveRepoForTest(&rc); err != nil {
		t.Fatalf("save repo: %v", err)
	}

	// Resolve without any filters — should find all remote artifacts
	res, err := Resolve(ctx, reg, ws, ResolveOpts{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	remoteRules := 0
	var ruleNames []string
	for _, r := range res.Rules {
		if r.Source != "local" {
			remoteRules++
			ruleNames = append(ruleNames, r.Name)
		}
	}
	if remoteRules != 2 {
		t.Fatalf("expected 2 remote rules from generic/ layout, got %d: %v", remoteRules, ruleNames)
	}

	remoteSkills := 0
	for _, s := range res.Skills {
		if s.Source != "local" {
			remoteSkills++
		}
	}
	if remoteSkills != 1 {
		t.Fatalf("expected 1 remote skill from generic/ layout, got %d", remoteSkills)
	}

	// Verify routing applied: architecture rules should be always_apply
	for _, r := range res.Rules {
		if r.Name == "solid-principles" && !r.AlwaysApply {
			t.Fatalf("solid-principles should have always_apply=true via routing")
		}
	}
}

// TestResolveGenericLayoutWithActiveFile verifies that smart routing works
// correctly with the generic/ layout — always_apply rules survive file filtering.
func TestResolveGenericLayoutWithActiveFile(t *testing.T) {
	ctx := context.Background()
	reg := setupTestRegistry(t)
	ws := setupTestWorkspace(t)

	url := "https://test.example.com/generic-filtered"
	lexDir := reg.LexiconDirForURL(url)

	rulesDir := filepath.Join(lexDir, "generic", "rules")
	os.MkdirAll(rulesDir, 0o755)

	os.WriteFile(filepath.Join(rulesDir, "always-rule.md"), []byte(
		"---\nid: always-rule\ntitle: Always Rule\nlabels: [architecture]\n---\nAlways applies.",
	), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "test-only-rule.md"), []byte(
		"---\nid: test-only-rule\ntitle: Test Only\nlabels: [testing]\n---\nOnly for tests.",
	), 0o644)

	os.WriteFile(filepath.Join(lexDir, "lexicon.yaml"), []byte(strings.Join([]string{
		"name: test-filtered",
		"version: \"1.0\"",
		"routing:",
		"  - match: { labels: [architecture] }",
		"    always_apply: true",
		"  - match: { labels: [testing] }",
		"    globs: [\"*_test.go\"]",
	}, "\n")+"\n"), 0o644)

	rc := registry.RepoConfig{URL: url, Priority: 25, Enabled: true}
	if err := reg.SaveRepoForTest(&rc); err != nil {
		t.Fatalf("save repo: %v", err)
	}

	// With active file that is NOT a test file, only always_apply should survive
	res, err := Resolve(ctx, reg, ws, ResolveOpts{ActiveFile: "pkg/main.go"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var remoteNames []string
	for _, r := range res.Rules {
		if r.Source != "local" {
			remoteNames = append(remoteNames, r.Name)
		}
	}
	if len(remoteNames) != 1 || remoteNames[0] != "always-rule" {
		t.Fatalf("expected only always-rule for non-test file, got %v", remoteNames)
	}

	// With a test file, both should appear
	res, err = Resolve(ctx, reg, ws, ResolveOpts{ActiveFile: "pkg/foo_test.go"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	remoteNames = nil
	for _, r := range res.Rules {
		if r.Source != "local" {
			remoteNames = append(remoteNames, r.Name)
		}
	}
	if len(remoteNames) != 2 {
		t.Fatalf("expected 2 rules for test file, got %v", remoteNames)
	}
}

func findLexiconDir(t *testing.T, reg *registry.Registry, sourceName string) string {
	t.Helper()
	sources, _ := reg.Load()
	for _, s := range sources {
		if strings.Contains(s.URL, sourceName) {
			return s.LocalPath
		}
	}
	t.Fatalf("source %q not found", sourceName)
	return ""
}
