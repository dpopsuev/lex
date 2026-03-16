//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Deterministic MCP Tests (fast, no LLM) ---

func TestE2E_Deterministic(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not found")
	}

	lexiconPath := envOr("LEXICON_PATH", "/home/dpopsuev/Workspace/lexicon")
	workspacePath := envOr("WORKSPACE_PATH", "/home/dpopsuev/Workspace/origami")

	if _, err := os.Stat(filepath.Join(lexiconPath, "lexicon.yaml")); err != nil {
		t.Skipf("lexicon repo not found at %s", lexiconPath)
	}

	stopContainer(t)
	t.Cleanup(func() { stopContainer(t) })

	buildImage(t)
	startContainer(t,
		lexiconPath+":/lexicon:ro,z",
		workspacePath+":/workspace:ro,z",
	)
	waitHealthy(t, 30*time.Second)

	sid := initSession(t)
	callID := 10
	next := func() int { callID++; return callID }

	t.Run("add_source", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "add", "url": "file:///lexicon",
		})
		if !strings.Contains(text, "file:///lexicon") {
			t.Fatalf("add response missing URL:\n%s", truncate(text, 300))
		}
	})

	t.Run("inspect_source", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "inspect", "url": "file:///lexicon",
		})
		if !strings.Contains(text, "rule") {
			t.Fatalf("inspect missing rule type:\n%s", truncate(text, 500))
		}
	})

	t.Run("list_sources", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "list",
		})
		if !strings.Contains(text, "file:///lexicon") {
			t.Fatalf("list missing source:\n%s", text)
		}
	})

	t.Run("resolve_all", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "resolve", "path": "/workspace",
		})
		var res map[string]any
		json.Unmarshal([]byte(text), &res)
		rules, _ := res["rules"].([]any)
		if len(rules) < 3 {
			t.Fatalf("expected >=3 rules, got %d", len(rules))
		}
		t.Logf("resolved %d rules", len(rules))
	})

	t.Run("resolve_with_context_signals", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action":   "resolve",
			"path":     "/workspace",
			"language": "go",
			"keywords": []string{"security"},
			"budget":   1000,
		})
		if !strings.Contains(text, "rules") {
			t.Fatalf("resolve with signals missing rules:\n%s", truncate(text, 300))
		}
	})

	t.Run("search", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "search", "query": "security",
		})
		var res map[string]any
		json.Unmarshal([]byte(text), &res)
		count, _ := res["count"].(float64)
		if count < 1 {
			t.Fatalf("expected >=1 search match, got %v", count)
		}
		t.Logf("search 'security': %d matches", int(count))
	})

	t.Run("search_no_matches", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "search", "query": "xyznonexistent",
		})
		var res map[string]any
		json.Unmarshal([]byte(text), &res)
		count, _ := res["count"].(float64)
		if count != 0 {
			t.Fatalf("expected 0 matches, got %v", count)
		}
	})

	t.Run("routing_by_file", func(t *testing.T) {
		allText := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "resolve", "path": "/workspace",
		})
		routedText := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "resolve", "path": "/workspace",
			"active_file": "pkg/foo_test.go",
		})
		var allRes, routedRes map[string]any
		json.Unmarshal([]byte(allText), &allRes)
		json.Unmarshal([]byte(routedText), &routedRes)
		allRules, _ := allRes["rules"].([]any)
		routedRules, _ := routedRes["rules"].([]any)

		if len(routedRules) >= len(allRules) {
			t.Fatalf("routing should filter: %d routed >= %d total", len(routedRules), len(allRules))
		}
		t.Logf("routing filtered %d → %d rules", len(allRules), len(routedRules))
	})

	t.Run("sync", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "sync",
		})
		if !strings.Contains(text, "synced") {
			t.Fatalf("sync response: %s", text)
		}
	})

	t.Run("remove_and_verify", func(t *testing.T) {
		mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "remove", "url": "file:///lexicon",
		})
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "resolve", "path": "/workspace",
		})
		var res map[string]any
		json.Unmarshal([]byte(text), &res)
		rules, _ := res["rules"].([]any)
		for _, r := range rules {
			rm, _ := r.(map[string]any)
			if rm["source"] != "local" {
				t.Fatalf("non-local rule survived removal: %v", rm["name"])
			}
		}
		t.Log("confirmed: only local rules remain after removal")
	})
}
