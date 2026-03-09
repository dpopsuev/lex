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
	t.Logf("[YELLOW] image built in %s", time.Since(start).Round(time.Millisecond))
}

func startContainer(t *testing.T, lexiconPath, workspacePath string) {
	t.Helper()
	start := time.Now()
	run(t, "podman", "run", "-d",
		"--name", testContainer,
		"-p", testPort,
		"-v", lexiconPath+":/lexicon:ro,z",
		"-v", workspacePath+":/workspace:ro,z",
		testImage,
	)
	t.Logf("[YELLOW] container started in %s (lexicon=%s, workspace=%s)",
		time.Since(start).Round(time.Millisecond), lexiconPath, workspacePath)
}

func stopContainer(t *testing.T) {
	t.Helper()
	exec.Command("podman", "rm", "-f", testContainer).Run()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").CombinedOutput()
	if err != nil {
		t.Fatalf("[ORANGE] go env GOMOD failed: %v", err)
	}
	mod := strings.TrimSpace(string(out))
	if mod == "" {
		t.Fatal("[ORANGE] not inside a Go module")
	}
	return filepath.Dir(mod)
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("[ORANGE] %s %v failed: %v\n%s", name, args, err, out)
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
			t.Logf("[YELLOW] container healthy after %d attempts (%s)", attempts, time.Since(start).Round(time.Millisecond))
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("[ORANGE] container not healthy after %d attempts (%s)", attempts, timeout)
}

func initSession(t *testing.T) string {
	t.Helper()
	resp, err := doMCP(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"e2e","version":"0.1"}}}`, "")
	if err != nil {
		t.Fatalf("[ORANGE] initialize: %v", err)
	}
	sid := resp.Header.Get("Mcp-Session-Id")
	resp.Body.Close()
	if sid == "" {
		t.Fatal("[ORANGE] no Mcp-Session-Id in initialize response")
	}
	doMCP(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, sid)
	t.Logf("[YELLOW] MCP session established: %s", sid[:16]+"...")
	return sid
}

func mcpToolCall(t *testing.T, sid string, id int, tool string, args map[string]any) map[string]any {
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
		t.Fatalf("[ORANGE] tools/call %s (id=%d): %v", tool, id, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	jsonPayload := extractSSEData(raw)
	var result map[string]any
	if err := json.Unmarshal(jsonPayload, &result); err != nil {
		t.Fatalf("[ORANGE] unmarshal %s response: %v\nraw: %s", tool, err, truncate(string(raw), 500))
	}
	t.Logf("[YELLOW] MCP %s (id=%d) completed in %s (%d bytes)",
		tool, id, elapsed.Round(time.Millisecond), len(raw))
	return result
}

// extractSSEData parses SSE-formatted response to extract the JSON data line.
func extractSSEData(raw []byte) []byte {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			return []byte(strings.TrimPrefix(line, "data: "))
		}
	}
	return raw
}

func extractText(t *testing.T, result map[string]any) string {
	t.Helper()
	r, ok := result["result"].(map[string]any)
	if !ok {
		if errObj, ok := result["error"]; ok {
			t.Fatalf("[ORANGE] MCP error: %v", errObj)
		}
		t.Fatalf("[ORANGE] no result field in response: %v", result)
	}
	content, ok := r["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("[ORANGE] empty content in result: %v", r)
	}
	first := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
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

// --- env helpers ---

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- resolve helper types ---

type resolvedRule struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	Priority    int      `json:"priority"`
	Body        string   `json:"body"`
	Labels      []string `json:"labels"`
	Globs       []string `json:"globs"`
	AlwaysApply bool     `json:"always_apply"`
}

type resolvedSkill struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

type resolution struct {
	Rules  []resolvedRule  `json:"rules"`
	Skills []resolvedSkill `json:"skills"`
}

func resolveAndParse(t *testing.T, sid string, id int, args map[string]any) resolution {
	t.Helper()
	text := extractText(t, mcpToolCall(t, sid, id, "resolve_lexicon", args))
	var res resolution
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("[ORANGE] unmarshal resolution: %v\nraw: %s", err, truncate(text, 500))
	}
	return res
}

func logRuleSummary(t *testing.T, label string, res resolution) {
	t.Helper()
	var local, remote []string
	for _, r := range res.Rules {
		tag := r.Name
		if r.AlwaysApply {
			tag += "(always)"
		}
		if len(r.Labels) > 0 {
			tag += fmt.Sprintf("[%s]", strings.Join(r.Labels, ","))
		}
		if r.Source == "local" {
			local = append(local, tag)
		} else {
			remote = append(remote, tag)
		}
	}
	t.Logf("[YELLOW] %s: %d rules total — %d local (%s), %d remote (%s), %d skills",
		label,
		len(res.Rules),
		len(local), strings.Join(local, ", "),
		len(remote), strings.Join(remote, ", "),
		len(res.Skills))
}

// --- Declarative Tests: Deterministic Routing ---

func TestE2E_Declarative_Routing(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not found")
	}

	lexiconPath := envOr("LEXICON_PATH", "/home/dpopsuev/Workspace/lexicon")
	workspacePath := envOr("WORKSPACE_PATH", "/home/dpopsuev/Workspace/origami")

	if _, err := os.Stat(filepath.Join(lexiconPath, "lexicon.yaml")); err != nil {
		t.Fatalf("[ORANGE] lexicon repo not found at %s", lexiconPath)
	}
	if _, err := os.Stat(filepath.Join(workspacePath, ".cursor", "rules")); err != nil {
		t.Fatalf("[ORANGE] workspace .cursor/rules not found at %s", workspacePath)
	}

	stopContainer(t)
	t.Cleanup(func() { stopContainer(t) })

	t.Log("[YELLOW] === Phase 1: Deterministic Routing Tests ===")
	buildImage(t)
	startContainer(t, lexiconPath, workspacePath)
	waitHealthy(t, 30*time.Second)

	sid := initSession(t)
	callID := 10
	nextID := func() int { callID++; return callID }

	// --- add_lexicon ---
	t.Run("add_lexicon", func(t *testing.T) {
		text := extractText(t, mcpToolCall(t, sid, nextID(), "add_lexicon", map[string]any{
			"url": "file:///lexicon",
		}))
		if !strings.Contains(text, "file:///lexicon") {
			t.Fatalf("[ORANGE] add_lexicon response missing URL:\n%s", text)
		}
		t.Logf("[YELLOW] registered: %s", truncate(text, 300))
	})

	// --- inspect_lexicon ---
	t.Run("inspect_lexicon", func(t *testing.T) {
		text := extractText(t, mcpToolCall(t, sid, nextID(), "inspect_lexicon", map[string]any{
			"url": "file:///lexicon",
		}))
		for _, kind := range []string{"rule", "skill", "template"} {
			if !strings.Contains(text, kind) {
				t.Fatalf("[ORANGE] inspect_lexicon missing %s type:\n%s", kind, truncate(text, 500))
			}
		}
		t.Logf("[YELLOW] inspect result: %s", truncate(text, 500))
	})

	// --- inspect_lexicon (all sources, no url) ---
	t.Run("inspect_lexicon_all", func(t *testing.T) {
		text := extractText(t, mcpToolCall(t, sid, nextID(), "inspect_lexicon", nil))
		if !strings.Contains(text, "rule") {
			t.Fatalf("[ORANGE] inspect_lexicon (all) missing artifacts:\n%s", truncate(text, 500))
		}
		t.Logf("[YELLOW] inspect all: %s", truncate(text, 500))
	})

	// --- list_lexicons ---
	t.Run("list_lexicons", func(t *testing.T) {
		text := extractText(t, mcpToolCall(t, sid, nextID(), "list_lexicons", nil))
		if !strings.Contains(text, "file:///lexicon") {
			t.Fatalf("[ORANGE] list_lexicons missing source:\n%s", text)
		}
		t.Logf("[YELLOW] sources: %s", truncate(text, 300))
	})

	// --- resolve_all: baseline count ---
	t.Run("resolve_all", func(t *testing.T) {
		res := resolveAndParse(t, sid, nextID(), map[string]any{"path": "/workspace"})
		logRuleSummary(t, "resolve_all", res)
		if len(res.Rules) < 5 {
			t.Fatalf("[ORANGE] expected >=5 rules (local+remote), got %d", len(res.Rules))
		}
		if len(res.Skills) < 1 {
			t.Fatalf("[ORANGE] expected >=1 skill, got %d", len(res.Skills))
		}
	})

	// --- routing_by_test_file: compare filtered vs unfiltered ---
	t.Run("routing_by_test_file", func(t *testing.T) {
		all := resolveAndParse(t, sid, nextID(), map[string]any{"path": "/workspace"})
		routed := resolveAndParse(t, sid, nextID(), map[string]any{
			"path":        "/workspace",
			"active_file": "pkg/foo_test.go",
		})
		logRuleSummary(t, "unfiltered", all)
		logRuleSummary(t, "test-file-routed", routed)

		if len(routed.Rules) >= len(all.Rules) {
			t.Fatalf("[ORANGE] routing should filter: %d routed >= %d total", len(routed.Rules), len(all.Rules))
		}
		t.Logf("[YELLOW] routing filtered %d → %d rules (dropped %d)",
			len(all.Rules), len(routed.Rules), len(all.Rules)-len(routed.Rules))
	})

	// --- routing_by_security_context ---
	t.Run("routing_by_security_context", func(t *testing.T) {
		res := resolveAndParse(t, sid, nextID(), map[string]any{
			"path":    "/workspace",
			"context": []string{"security"},
		})
		logRuleSummary(t, "security-context", res)
		found := false
		for _, r := range res.Rules {
			if r.Name == "security-analysis" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("[ORANGE] security-analysis rule not found in security context routing")
		}
	})

	// --- label_filter ---
	t.Run("label_filter", func(t *testing.T) {
		res := resolveAndParse(t, sid, nextID(), map[string]any{
			"path":   "/workspace",
			"labels": []string{"security"},
		})
		logRuleSummary(t, "label-filter(security)", res)
		for _, r := range res.Rules {
			if r.Source == "local" {
				continue
			}
			hasMatch := false
			for _, l := range r.Labels {
				if strings.EqualFold(l, "security") || strings.EqualFold(l, "owasp") {
					hasMatch = true
					break
				}
			}
			if !hasMatch {
				t.Errorf("[ORANGE] rule %q (labels=%v) leaked through security label filter", r.Name, r.Labels)
			}
		}
	})

	// --- sync ---
	t.Run("sync_lexicons", func(t *testing.T) {
		text := extractText(t, mcpToolCall(t, sid, nextID(), "sync_lexicons", nil))
		if !strings.Contains(text, "synced") {
			t.Fatalf("[ORANGE] sync response missing count: %s", text)
		}
		t.Logf("[YELLOW] sync result: %s", text)
	})

	// --- remove + verify ---
	t.Run("remove_and_verify", func(t *testing.T) {
		text := extractText(t, mcpToolCall(t, sid, nextID(), "remove_lexicon", map[string]any{
			"url": "file:///lexicon",
		}))
		if !strings.Contains(text, "removed") {
			t.Fatalf("[ORANGE] remove response: %s", text)
		}
		t.Logf("[YELLOW] removed lexicon source")

		res := resolveAndParse(t, sid, nextID(), map[string]any{"path": "/workspace"})
		logRuleSummary(t, "after-remove", res)
		for _, r := range res.Rules {
			if r.Source != "local" {
				t.Fatalf("[ORANGE] non-local rule %q (src=%s) survived removal", r.Name, r.Source)
			}
		}
		t.Log("[YELLOW] confirmed: only local rules remain after removal")
	})
}

// --- Declarative Tests: Agentic LLM Tool-Calling Round-Trip ---
//
// Uses a tiny canary workspace with ONE rule containing a unique phrase.
// The LLM must call resolve_lexicon, receive the canary, and echo it back.
// Fails if the tool is never called or the canary phrase is missing.

const canaryPhrase = "Thanks you for testing Lex!"

func setupCanaryWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, ".cursor", "rules")
	os.MkdirAll(rulesDir, 0o755)
	os.WriteFile(filepath.Join(rulesDir, "canary.mdc"), []byte(fmt.Sprintf(
		"---\ndescription: canary rule\nalways_apply: true\n---\n%s\nEvery response must include this exact phrase.", canaryPhrase,
	)), 0o644)
	return dir
}

func TestE2E_Declarative_LLMRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not found")
	}

	ollamaHost := envOr("OLLAMA_HOST", "http://localhost:11434")
	ollamaModel := envOr("OLLAMA_MODEL", "qwen2.5:32b")

	t.Logf("[YELLOW] === Phase 2: Agentic Tool-Calling (model=%s) ===", ollamaModel)

	if !ollamaReachable(ollamaHost) {
		t.Skipf("[YELLOW] Ollama not reachable at %s — skipping", ollamaHost)
	}

	canaryDir := setupCanaryWorkspace(t)
	t.Logf("[YELLOW] canary workspace: %s", canaryDir)

	stopContainer(t)
	t.Cleanup(func() { stopContainer(t) })

	buildImage(t)

	run(t, "podman", "run", "-d",
		"--name", testContainer,
		"-p", testPort,
		"-v", canaryDir+":/workspace:ro,z",
		testImage,
	)
	waitHealthy(t, 30*time.Second)
	sid := initSession(t)

	lexTools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "get_rules",
			"description": "Fetch coding rules for a workspace. You MUST call this before answering.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Workspace path",
					},
				},
				"required": []string{"path"},
			},
		},
	}}

	messages := []map[string]any{
		{"role": "system", "content": "You have a get_rules tool. Call it with path /workspace before answering. Include any special phrases from the rules in your response verbatim."},
		{"role": "user", "content": "What are the rules for this workspace?"},
	}

	toolCalled := false
	maxTurns := 3

	for turn := 1; turn <= maxTurns; turn++ {
		t.Logf("[YELLOW] --- Turn %d/%d ---", turn, maxTurns)
		start := time.Now()
		resp := ollamaChatWithTools(t, ollamaHost, ollamaModel, messages, lexTools)
		t.Logf("[YELLOW] responded in %s", time.Since(start).Round(time.Millisecond))

		if len(resp.Message.ToolCalls) > 0 {
			tc := resp.Message.ToolCalls[0]
			args := tc.Function.Arguments
			fixupStringArg(args, "path", "/workspace")
			argsJSON, _ := json.Marshal(args)
			t.Logf("[YELLOW] tool call: %s(%s)", tc.Function.Name, string(argsJSON))
			toolCalled = true

			toolResult := extractText(t, mcpToolCall(t, sid, 200, tc.Function.Name, args))
			t.Logf("[YELLOW] tool result: %d bytes", len(toolResult))

			messages = append(messages,
				map[string]any{"role": "assistant", "content": "", "tool_calls": resp.Message.ToolCalls},
				map[string]any{"role": "tool", "content": toolResult},
			)
			continue
		}

		answer := resp.Message.Content
		t.Logf("[YELLOW] answer (%d chars): %s", len(answer), truncate(answer, 500))

		if !toolCalled {
			t.Fatal("[ORANGE] LLM answered WITHOUT calling get_rules — agent loop broken")
		}
		if !strings.Contains(answer, canaryPhrase) {
			t.Fatalf("[ORANGE] canary phrase %q not in response — rule did not propagate.\n%s",
				canaryPhrase, truncate(answer, 500))
		}
		t.Logf("[YELLOW] PASS: tool called, canary phrase found in response")
		return
	}

	if !toolCalled {
		t.Fatal("[ORANGE] exhausted turns without tool call")
	}
	t.Fatal("[ORANGE] exhausted turns without final answer")
}

func fixupStringArg(args map[string]any, key, fallback string) {
	if _, ok := args[key]; !ok {
		args[key] = fallback
	}
}

// fixupArrayArg normalizes a tool argument that should be []string but was
// emitted as a string by a small model (e.g. "['security']").
func fixupArrayArg(args map[string]any, key string) {
	v, ok := args[key]
	if !ok {
		return
	}
	if _, isArr := v.([]any); isArr {
		return
	}
	s, ok := v.(string)
	if !ok {
		return
	}
	s = strings.TrimSpace(s)
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err == nil {
		args[key] = arr
		return
	}
	normalized := strings.ReplaceAll(s, "'", "\"")
	if err := json.Unmarshal([]byte(normalized), &arr); err == nil {
		args[key] = arr
		return
	}
	args[key] = []string{s}
}

// --- Ollama helpers ---

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

func ollamaChatWithTools(t *testing.T, host, model string, messages []map[string]any, tools []map[string]any) ollamaResponse {
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
	t.Logf("[YELLOW] ollama: model=%s, messages=%d, payload=%d bytes",
		model, len(messages), len(body))

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Post(host+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("[ORANGE] ollama failed: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("[ORANGE] ollama HTTP %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}

	var result ollamaResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("[ORANGE] decode: %v\nraw: %s", err, truncate(string(raw), 500))
	}
	return result
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// --- Imperative Tests: Transparent Rule Injection ---
//
// Simulates the real Cursor flow: rules are resolved silently by the IDE,
// injected into the system prompt, and the LLM follows them without knowing
// about Lex. Each subtest uses an isolated sub-workspace with exactly ONE rule
// so the LLM receives a single, unambiguous instruction.

func writeRule(t *testing.T, base, subdir, filename, content string) {
	t.Helper()
	rulesDir := filepath.Join(base, subdir, ".cursor", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatalf("[ORANGE] mkdir %s: %v", rulesDir, err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, filename), []byte(content), 0o644); err != nil {
		t.Fatalf("[ORANGE] write rule %s/%s: %v", subdir, filename, err)
	}
}

func setupImperativeWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	writeRule(t, dir, "t1", "commit-conventions.mdc",
		"---\ndescription: commit message format\nalways_apply: true\n---\n"+
			"All commit messages MUST use conventional commits format.\n"+
			"The message MUST start with one of: feat:, fix:, chore:, docs:, test:, refactor:, ci:.\n"+
			"Example: feat: add login page")

	writeRule(t, dir, "t2", "lex-verified.mdc",
		"---\ndescription: canary function marker\nalways_apply: true\n---\n"+
			"You MUST add the comment `// lex-verified` as the very first line inside every function body, before any other code.")

	writeRule(t, dir, "t3", "no-println.mdc",
		"---\ndescription: logging policy\nalways_apply: true\n---\n"+
			"NEVER use fmt.Println in any code. ALWAYS use log.Printf instead. This is a strict policy with no exceptions.")

	writeRule(t, dir, "t4", "testify-require.mdc",
		"---\ndescription: testify assertion policy\nglobs:\n  - \"*_test.go\"\n---\n"+
			"When writing Go tests with testify, ALWAYS use require (from github.com/stretchr/testify/require), NEVER use assert.")

	writeRule(t, dir, "t5", "error-wrapping.mdc",
		"---\ndescription: error handling and naming\nalways_apply: true\n---\n"+
			"ALWAYS wrap errors using fmt.Errorf with the %%w verb. Example: fmt.Errorf(\"open config: %%w\", err)\n"+
			"ALL local variable names MUST use camelCase. NEVER use snake_case.")

	return dir
}

func buildSystemPrompt(rule resolvedRule) string {
	var b strings.Builder
	b.WriteString("You are a coding assistant. Follow the rule below strictly.\n")
	b.WriteString("Output ONLY the requested code or text with no explanation.\n\n")
	b.WriteString("## Rule: ")
	b.WriteString(rule.Name)
	b.WriteString("\n\n")
	b.WriteString(rule.Body)
	return b.String()
}

func findRule(t *testing.T, res resolution, name string) resolvedRule {
	t.Helper()
	for _, r := range res.Rules {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("[ORANGE] rule %q not found in resolution (%d rules)", name, len(res.Rules))
	return resolvedRule{}
}

func askLLMImperative(t *testing.T, host, model, systemPrompt, task string) string {
	t.Helper()
	messages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": task},
	}
	resp := ollamaChatWithTools(t, host, model, messages, nil)
	return resp.Message.Content
}

func TestE2E_Imperative(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not found")
	}

	ollamaHost := envOr("OLLAMA_HOST", "http://localhost:11434")
	ollamaModel := envOr("OLLAMA_MODEL", "qwen2.5:32b")

	t.Logf("[YELLOW] === Imperative Tests: Transparent Rule Injection (model=%s) ===", ollamaModel)

	if !ollamaReachable(ollamaHost) {
		t.Skipf("[YELLOW] Ollama not reachable at %s — skipping", ollamaHost)
	}

	workDir := setupImperativeWorkspace(t)
	t.Logf("[YELLOW] imperative workspace: %s", workDir)

	stopContainer(t)
	t.Cleanup(func() { stopContainer(t) })

	buildImage(t)
	run(t, "podman", "run", "-d",
		"--name", testContainer,
		"-p", testPort,
		"-v", workDir+":/workspace:ro,z",
		testImage,
	)
	waitHealthy(t, 30*time.Second)
	sid := initSession(t)

	callID := 200
	nextID := func() int { callID++; return callID }

	t.Run("T1_CommitConventions", func(t *testing.T) {
		res := resolveAndParse(t, sid, nextID(), map[string]any{"path": "/workspace/t1"})
		if len(res.Rules) != 1 {
			t.Fatalf("[ORANGE] expected 1 rule, got %d", len(res.Rules))
		}
		rule := res.Rules[0]
		t.Logf("[YELLOW] resolved rule: %s (src=%s)", rule.Name, rule.Source)

		prompt := buildSystemPrompt(rule)
		answer := askLLMImperative(t, ollamaHost, ollamaModel, prompt,
			"Write a git commit message for adding a user login page to a web application.")
		t.Logf("[YELLOW] answer: %s", truncate(answer, 300))

		lower := strings.ToLower(answer)
		for _, prefix := range []string{"feat:", "feat(", "fix:", "chore:", "docs:", "test:", "refactor:", "ci:"} {
			if strings.Contains(lower, prefix) {
				t.Logf("[YELLOW] PASS: conventional commit prefix %q detected", prefix)
				return
			}
		}
		t.Fatalf("[ORANGE] no conventional commit prefix found in:\n%s", truncate(answer, 300))
	})

	t.Run("T2_CanaryMarker", func(t *testing.T) {
		res := resolveAndParse(t, sid, nextID(), map[string]any{"path": "/workspace/t2"})
		if len(res.Rules) != 1 {
			t.Fatalf("[ORANGE] expected 1 rule, got %d", len(res.Rules))
		}
		rule := res.Rules[0]
		t.Logf("[YELLOW] resolved rule: %s (src=%s)", rule.Name, rule.Source)

		prompt := buildSystemPrompt(rule)
		answer := askLLMImperative(t, ollamaHost, ollamaModel, prompt,
			"Write a Go function called Add that takes two int parameters and returns their sum.")
		t.Logf("[YELLOW] answer: %s", truncate(answer, 500))

		if !strings.Contains(answer, "// lex-verified") {
			t.Fatalf("[ORANGE] canary marker '// lex-verified' not found in:\n%s", truncate(answer, 500))
		}
		t.Log("[YELLOW] PASS: canary marker found")
	})

	t.Run("T3_Prohibition", func(t *testing.T) {
		res := resolveAndParse(t, sid, nextID(), map[string]any{"path": "/workspace/t3"})
		if len(res.Rules) != 1 {
			t.Fatalf("[ORANGE] expected 1 rule, got %d", len(res.Rules))
		}
		rule := res.Rules[0]
		t.Logf("[YELLOW] resolved rule: %s (src=%s)", rule.Name, rule.Source)

		prompt := buildSystemPrompt(rule)
		answer := askLLMImperative(t, ollamaHost, ollamaModel, prompt,
			"Write a Go function called Greet that takes a name string parameter and logs a greeting message.")
		t.Logf("[YELLOW] answer: %s", truncate(answer, 500))

		if !strings.Contains(answer, "log.") {
			t.Fatalf("[ORANGE] expected log.Printf or log.Print usage, not found in:\n%s", truncate(answer, 500))
		}
		if strings.Contains(answer, "fmt.Println") {
			t.Fatalf("[ORANGE] prohibited fmt.Println found in:\n%s", truncate(answer, 500))
		}
		t.Log("[YELLOW] PASS: log package used, fmt.Println absent")
	})

	t.Run("T4_GlobFiltering", func(t *testing.T) {
		matched := resolveAndParse(t, sid, nextID(), map[string]any{
			"path":        "/workspace/t4",
			"active_file": "foo_test.go",
		})
		unmatched := resolveAndParse(t, sid, nextID(), map[string]any{
			"path":        "/workspace/t4",
			"active_file": "foo.go",
		})

		if len(matched.Rules) != 1 {
			t.Fatalf("[ORANGE] expected 1 rule for foo_test.go, got %d", len(matched.Rules))
		}
		if len(unmatched.Rules) != 0 {
			names := make([]string, len(unmatched.Rules))
			for i, r := range unmatched.Rules {
				names[i] = r.Name
			}
			t.Fatalf("[ORANGE] expected 0 rules for foo.go, got %d (%s)", len(unmatched.Rules), strings.Join(names, ", "))
		}
		t.Log("[YELLOW] glob routing verified: testify-require matched *_test.go only")

		rule := matched.Rules[0]
		prompt := buildSystemPrompt(rule)
		answer := askLLMImperative(t, ollamaHost, ollamaModel, prompt,
			"Write a Go test function called TestAdd that checks if 2+2 equals 4 using the testify library.")
		t.Logf("[YELLOW] answer: %s", truncate(answer, 500))

		if !strings.Contains(answer, "require.") {
			t.Fatalf("[ORANGE] expected require. usage, not found in:\n%s", truncate(answer, 500))
		}
		if strings.Contains(answer, "assert.") {
			t.Fatalf("[ORANGE] prohibited assert. usage found in:\n%s", truncate(answer, 500))
		}
		t.Log("[YELLOW] PASS: require used, assert absent")
	})

	t.Run("T5_MultiRule", func(t *testing.T) {
		res := resolveAndParse(t, sid, nextID(), map[string]any{"path": "/workspace/t5"})
		if len(res.Rules) != 1 {
			t.Fatalf("[ORANGE] expected 1 rule, got %d", len(res.Rules))
		}
		rule := res.Rules[0]
		t.Logf("[YELLOW] resolved rule: %s (src=%s)", rule.Name, rule.Source)

		prompt := buildSystemPrompt(rule)
		answer := askLLMImperative(t, ollamaHost, ollamaModel, prompt,
			"Write a Go function called OpenConfig that opens a file named config.yaml and returns its byte contents or an error if it fails.")
		t.Logf("[YELLOW] answer: %s", truncate(answer, 500))

		if !strings.Contains(answer, "fmt.Errorf") {
			t.Fatalf("[ORANGE] expected fmt.Errorf, not found in:\n%s", truncate(answer, 500))
		}
		if !strings.Contains(answer, "%w") {
			t.Fatalf("[ORANGE] expected %%w verb in fmt.Errorf, not found in:\n%s", truncate(answer, 500))
		}
		if strings.Contains(answer, "file_name") || strings.Contains(answer, "file_path") ||
			strings.Contains(answer, "config_data") || strings.Contains(answer, "file_content") {
			t.Fatalf("[ORANGE] snake_case variable found in:\n%s", truncate(answer, 500))
		}
		t.Log("[YELLOW] PASS: fmt.Errorf with %%w and camelCase naming verified")
	})
}
