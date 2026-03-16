//go:build llm

package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

// agentLoop runs an LLM agent loop with structured instrumentation.
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
	ollamaModel = envOr("OLLAMA_MODEL", "qwen3:4b")
	if !ollamaReachable(ollamaHost) {
		t.Skipf("Ollama not reachable at %s", ollamaHost)
	}
	t.Logf("LLM: %s @ %s", ollamaModel, ollamaHost)

	lexiconPath := envOr("LEXICON_PATH", "/home/dpopsuev/Workspace/lexicon")
	workspacePath := repoRoot(t)

	stopContainer(t)
	t.Cleanup(func() { stopContainer(t) })
	buildImage(t)
	startContainer(t,
		lexiconPath+":/lexicon:ro,z",
		workspacePath+":/workspace:ro,z",
	)
	waitHealthy(t, 30*time.Second)
	sid = initSession(t)

	mcpCall(t, sid, 100, "lexicon", map[string]any{
		"action": "add", "url": "file:///lexicon",
	})
	return
}

// --- LLM Agent Round-Trip Tests (slow, requires Ollama) ---

func TestLLM_ResolveAndReport(t *testing.T) {
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
	if answer == "" {
		t.Error("expected non-empty final answer")
	}
}

func TestLLM_SearchAndExplain(t *testing.T) {
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

func TestLLM_ContextAwareResolve(t *testing.T) {
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

func TestLLM_SourceManagement(t *testing.T) {
	sid, host, model := setupLLMTest(t)

	tools, _ := agentLoop(t, sid, host, model,
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
}

// --- Imperative Tests: Transparent Rule Injection (slow, requires Ollama) ---

func writeRule(t *testing.T, base, subdir, filename, content string) {
	t.Helper()
	rulesDir := filepath.Join(base, subdir, ".cursor", "rules")
	os.MkdirAll(rulesDir, 0o755)
	os.WriteFile(filepath.Join(rulesDir, filename), []byte(content), 0o644)
}

func TestLLM_Imperative(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not found")
	}

	ollamaHost := envOr("OLLAMA_HOST", "http://localhost:11434")
	ollamaModel := envOr("OLLAMA_MODEL", "qwen3:4b")

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
