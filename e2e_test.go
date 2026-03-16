//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testImage     = "lex-e2e-test"
	testContainer = "lex-e2e-test"
	testAddr      = "http://localhost:18082"
	testPort      = "18082:8082"
)

// --- container lifecycle ---

func buildImage(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	start := time.Now()
	run(t, "podman", "build", "-t", testImage, "-f", filepath.Join(root, "Dockerfile.test"), root)
	t.Logf("image built in %s", time.Since(start).Round(time.Millisecond))
}

func startContainer(t *testing.T, mounts ...string) {
	t.Helper()
	start := time.Now()
	args := []string{"run", "-d", "--name", testContainer, "-p", testPort}
	for _, m := range mounts {
		args = append(args, "-v", m)
	}
	args = append(args, testImage)
	run(t, "podman", args...)
	t.Logf("container started in %s", time.Since(start).Round(time.Millisecond))
}

func stopContainer(t *testing.T) {
	t.Helper()
	exec.Command("podman", "rm", "-f", testContainer).Run()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").CombinedOutput()
	if err != nil {
		t.Fatalf("go env GOMOD failed: %v", err)
	}
	mod := strings.TrimSpace(string(out))
	if mod == "" {
		t.Fatal("not inside a Go module")
	}
	return filepath.Dir(mod)
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

// --- MCP helpers ---

func waitHealthy(t *testing.T, timeout time.Duration) {
	t.Helper()
	start := time.Now()
	deadline := time.Now().Add(timeout)
	body := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"e2e","version":"0.1"}}}`
	attempts := 0
	for time.Now().Before(deadline) {
		attempts++
		resp, err := doMCP(body, "")
		if err == nil {
			resp.Body.Close()
			t.Logf("container healthy after %d attempts (%s)", attempts, time.Since(start).Round(time.Millisecond))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("container not healthy after %d attempts (%s)", attempts, timeout)
}

func initSession(t *testing.T) string {
	t.Helper()
	resp, err := doMCP(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"e2e","version":"0.1"}}}`, "")
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid := resp.Header.Get("Mcp-Session-Id")
	resp.Body.Close()
	if sid == "" {
		t.Fatal("no Mcp-Session-Id in initialize response")
	}
	doMCP(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, sid)
	t.Logf("MCP session established: %s", sid[:16]+"...")
	return sid
}

func mcpCall(t *testing.T, sid string, id int, tool string, args map[string]any) string {
	t.Helper()
	params := map[string]any{"name": tool}
	if args != nil {
		params["arguments"] = args
	}
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  params,
	}
	body, _ := json.Marshal(req)
	start := time.Now()
	resp, err := doMCP(string(body), sid)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("tools/call %s (id=%d): %v", tool, id, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	jsonPayload := extractSSEData(raw)
	var result map[string]any
	if err := json.Unmarshal(jsonPayload, &result); err != nil {
		t.Fatalf("unmarshal %s response: %v\nraw: %s", tool, err, truncate(string(raw), 500))
	}
	t.Logf("MCP %s (id=%d) completed in %s (%d bytes)", tool, id, elapsed.Round(time.Millisecond), len(raw))

	r, ok := result["result"].(map[string]any)
	if !ok {
		if errObj, ok := result["error"]; ok {
			t.Fatalf("MCP error: %v", errObj)
		}
		t.Fatalf("no result field: %v", result)
	}
	content, ok := r["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("empty content: %v", r)
	}
	first := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

func extractSSEData(raw []byte) []byte {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			return []byte(strings.TrimPrefix(line, "data: "))
		}
	}
	return raw
}

func doMCP(body, sid string) (*http.Response, error) {
	req, _ := http.NewRequest("POST", testAddr+"/", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)
	}
	return resp, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// --- Deterministic MCP Tests ---

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
		if !strings.Contains(text, "rules") {
			t.Fatalf("resolve missing rules:\n%s", truncate(text, 300))
		}
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
		t.Logf("resolve with context signals: %s", truncate(text, 200))
	})

	t.Run("search", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "search", "query": "security",
		})
		if !strings.Contains(text, "matches") {
			t.Fatalf("search missing matches field:\n%s", truncate(text, 300))
		}
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
			t.Fatalf("expected 0 matches for nonsense query, got %v", count)
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

// --- LLM Round-Trip Tests ---

type ollamaToolCall struct {
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaResponse struct {
	Message ollamaMessage `json:"message"`
}

func ollamaReachable(host string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(host + "/api/tags")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func ollamaChat(t *testing.T, host, model string, messages []map[string]any, tools []map[string]any) ollamaResponse {
	t.Helper()
	payload := map[string]any{
		"model":    model,
		"stream":   false,
		"messages": messages,
		"options":  map[string]any{"temperature": 0.0},
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	body, _ := json.Marshal(payload)
	t.Logf("ollama: model=%s, messages=%d, payload=%d bytes", model, len(messages), len(body))

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Post(host+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("ollama failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("ollama HTTP %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}
	var result ollamaResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode: %v\nraw: %s", err, truncate(string(raw), 500))
	}
	return result
}

func lexTools() []map[string]any {
	return []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "lexicon",
				"description": "Resolve rules/skills, search for content, manage sources.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action":      map[string]any{"type": "string", "description": "resolve, search, inspect, add, remove, sync, list"},
						"path":        map[string]any{"type": "string", "description": "Workspace path"},
						"query":       map[string]any{"type": "string", "description": "Search query string"},
						"language":    map[string]any{"type": "string", "description": "Programming language"},
						"active_file": map[string]any{"type": "string", "description": "Currently active file"},
						"context":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Context keywords"},
						"labels":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Label filter"},
						"url":         map[string]any{"type": "string", "description": "Lexicon repository URL"},
						"budget":      map[string]any{"type": "integer", "description": "Token budget"},
					},
					"required": []string{"action"},
				},
			},
		},
	}
}

func agentLoop(t *testing.T, sid, ollamaHost, ollamaModel, systemPrompt, userPrompt string, maxTurns int) (toolsCalled []string, finalAnswer string) {
	t.Helper()
	tools := lexTools()
	messages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": userPrompt},
	}
	callID := 500

	for turn := 1; turn <= maxTurns; turn++ {
		start := time.Now()
		resp := ollamaChat(t, ollamaHost, ollamaModel, messages, tools)
		elapsed := time.Since(start)

		if len(resp.Message.ToolCalls) == 0 {
			finalAnswer = resp.Message.Content
			t.Logf("=== Turn %d/%d === FINAL ANSWER (%.1fs)\n  %s",
				turn, maxTurns, elapsed.Seconds(), truncate(finalAnswer, 300))
			return
		}

		for _, tc := range resp.Message.ToolCalls {
			callID++
			toolsCalled = append(toolsCalled, tc.Function.Name)
			argsJSON, _ := json.Marshal(tc.Function.Arguments)

			result := mcpCall(t, sid, callID, tc.Function.Name, tc.Function.Arguments)

			isError := strings.Contains(result, "error")
			errorTag := ""
			if isError {
				errorTag = " ⚠ ERROR"
			}

			t.Logf("=== Turn %d/%d === TOOL CALL (%.1fs)%s\n  CALL: %s(%s)\n  RESULT: %s",
				turn, maxTurns, elapsed.Seconds(), errorTag,
				tc.Function.Name, truncate(string(argsJSON), 150),
				truncate(result, 200))

			messages = append(messages,
				map[string]any{"role": "assistant", "content": "", "tool_calls": []ollamaToolCall{tc}},
				map[string]any{"role": "tool", "content": result},
			)
		}
	}
	t.Fatalf("exhausted %d turns without final answer (tools called: %v)", maxTurns, toolsCalled)
	return
}

func setupLLMTest(t *testing.T) (sid, ollamaHost, ollamaModel string) {
	t.Helper()
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not found")
	}
	ollamaHost = envOr("OLLAMA_HOST", "http://localhost:11434")
	ollamaModel = envOr("OLLAMA_MODEL", "qwen3:1.7b")
	if !ollamaReachable(ollamaHost) {
		t.Skipf("Ollama not reachable at %s", ollamaHost)
	}
	t.Logf("LLM: %s @ %s", ollamaModel, ollamaHost)

	lexiconPath := envOr("LEXICON_PATH", "/home/dpopsuev/Workspace/lexicon")
	workspacePath := repoRoot(t) // use Lex repo itself as workspace

	stopContainer(t)
	t.Cleanup(func() { stopContainer(t) })
	buildImage(t)
	startContainer(t,
		lexiconPath+":/lexicon:ro,z",
		workspacePath+":/workspace:ro,z",
	)
	waitHealthy(t, 30*time.Second)
	sid = initSession(t)

	// Register lexicon source
	mcpCall(t, sid, 100, "lexicon", map[string]any{
		"action": "add", "url": "file:///lexicon",
	})
	return
}

// --- LLM Test Scenarios ---

func TestE2E_LLM_ResolveAndReport(t *testing.T) {
	sid, host, model := setupLLMTest(t)

	tools, answer := agentLoop(t, sid, host, model,
		"You call tools exactly as instructed. Follow the steps precisely. Do not think, just act. /no_think",
		`Do these steps in order:
Step 1: Call lexicon with {"action":"resolve","path":"/workspace","budget":200}
Step 2: Say "done" and report how many rules were in the result.`,
		4,
	)

	if len(tools) < 1 {
		t.Fatalf("expected at least 1 tool call, got %d: %v", len(tools), tools)
	}
	// Model should report something from the resolve result (count, names, or "rule").
	if answer == "" {
		t.Error("expected non-empty final answer")
	}
}

func TestE2E_LLM_SearchAndExplain(t *testing.T) {
	sid, host, model := setupLLMTest(t)

	tools, answer := agentLoop(t, sid, host, model,
		"You call tools exactly as instructed. Follow the steps precisely. Do not think, just act. /no_think",
		`Do these steps in order:
Step 1: Call lexicon with {"action":"search","query":"security"}
Step 2: Report what you found — how many matches and their names.`,
		4,
	)

	if len(tools) < 1 {
		t.Fatalf("expected at least 1 tool call, got %d: %v", len(tools), tools)
	}
	lower := strings.ToLower(answer)
	if !strings.Contains(lower, "security") {
		t.Errorf("answer should mention security matches: %s", truncate(answer, 300))
	}
}

func TestE2E_LLM_ContextAwareResolve(t *testing.T) {
	sid, host, model := setupLLMTest(t)

	tools, answer := agentLoop(t, sid, host, model,
		"You call tools exactly as instructed. Follow the steps precisely. Do not think, just act. /no_think",
		`Do these steps in order:
Step 1: Call lexicon with {"action":"resolve","path":"/workspace","language":"go","budget":200}
Step 2: Say "done" and list the rule names from the result.`,
		4,
	)

	if len(tools) < 1 {
		t.Fatalf("expected at least 1 tool call, got %d: %v", len(tools), tools)
	}
	if answer == "" {
		t.Error("expected non-empty final answer")
	}
}

func TestE2E_LLM_SourceManagement(t *testing.T) {
	sid, host, model := setupLLMTest(t)

	tools, answer := agentLoop(t, sid, host, model,
		"You call tools exactly as instructed. Follow the steps precisely. Do not think, just act. /no_think",
		`Do these steps in order:
Step 1: Call lexicon with {"action":"list"} to see registered sources.
Step 2: Call lexicon with {"action":"sync"} to re-fetch all sources.
Step 3: Report what sources are registered and how many were synced.`,
		5,
	)

	if len(tools) < 2 {
		t.Fatalf("expected at least 2 tool calls, got %d: %v", len(tools), tools)
	}
	_ = answer
}

// --- Imperative Tests: Transparent Rule Injection ---

func writeRule(t *testing.T, base, subdir, filename, content string) {
	t.Helper()
	rulesDir := filepath.Join(base, subdir, ".cursor", "rules")
	os.MkdirAll(rulesDir, 0o755)
	os.WriteFile(filepath.Join(rulesDir, filename), []byte(content), 0o644)
}

func TestE2E_Imperative(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not found")
	}

	ollamaHost := envOr("OLLAMA_HOST", "http://localhost:11434")
	ollamaModel := envOr("OLLAMA_MODEL", "qwen3:1.7b")

	if !ollamaReachable(ollamaHost) {
		t.Skipf("Ollama not reachable at %s", ollamaHost)
	}

	workDir := t.TempDir()
	writeRule(t, workDir, "t1", "commit-conventions.mdc",
		"---\ndescription: commit message format\nalwaysApply: true\n---\n"+
			"All commit messages MUST use conventional commits format.\n"+
			"The message MUST start with one of: feat:, fix:, chore:, docs:, test:, refactor:, ci:.\n"+
			"Example: feat: add login page")

	writeRule(t, workDir, "t2", "lex-verified.mdc",
		"---\ndescription: canary function marker\nalwaysApply: true\n---\n"+
			"You MUST add the comment `// lex-verified` as the very first line inside every function body.")

	stopContainer(t)
	t.Cleanup(func() { stopContainer(t) })

	buildImage(t)
	startContainer(t, workDir+":/workspace:ro,z")
	waitHealthy(t, 30*time.Second)
	sid := initSession(t)
	callID := 200
	next := func() int { callID++; return callID }

	t.Run("ConventionalCommits", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "resolve", "path": "/workspace/t1",
		})
		var res map[string]any
		json.Unmarshal([]byte(text), &res)
		rules, _ := res["rules"].([]any)
		if len(rules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(rules))
		}
		r := rules[0].(map[string]any)
		ruleBody, _ := r["body"].(string)

		prompt := "You are a coding assistant. Follow the rule below strictly.\nOutput ONLY the requested text.\n\n" + ruleBody
		resp := ollamaChat(t, ollamaHost, ollamaModel,
			[]map[string]any{
				{"role": "system", "content": prompt},
				{"role": "user", "content": "Write a git commit message for adding a user login page."},
			}, nil)

		answer := resp.Message.Content
		lower := strings.ToLower(answer)
		for _, prefix := range []string{"feat:", "feat(", "fix:", "chore:", "docs:"} {
			if strings.Contains(lower, prefix) {
				t.Logf("PASS: conventional commit prefix %q found", prefix)
				return
			}
		}
		t.Fatalf("no conventional commit prefix in: %s", truncate(answer, 300))
	})

	t.Run("CanaryMarker", func(t *testing.T) {
		text := mcpCall(t, sid, next(), "lexicon", map[string]any{
			"action": "resolve", "path": "/workspace/t2",
		})
		var res map[string]any
		json.Unmarshal([]byte(text), &res)
		rules, _ := res["rules"].([]any)
		if len(rules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(rules))
		}
		r := rules[0].(map[string]any)
		ruleBody, _ := r["body"].(string)

		prompt := "You are a coding assistant. Follow the rule below strictly.\nOutput ONLY the code.\n\n" + ruleBody
		resp := ollamaChat(t, ollamaHost, ollamaModel,
			[]map[string]any{
				{"role": "system", "content": prompt},
				{"role": "user", "content": "Write a Go function called Add that takes two ints and returns their sum."},
			}, nil)

		if !strings.Contains(resp.Message.Content, "// lex-verified") {
			t.Fatalf("canary marker not found in: %s", truncate(resp.Message.Content, 500))
		}
		t.Log("PASS: canary marker found")
	})
}
