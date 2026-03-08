//go:build integration

package integration_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	image     = "lex-integration-test"
	container = "lex-integration-test"
	addr      = "http://localhost:18082"
	port      = "18082:8082"
)

func TestContainerLifecycle(t *testing.T) {
	requireCmd(t, "podman")
	cleanup(t)
	t.Cleanup(func() { cleanup(t) })

	repoRoot := findRepoRoot(t)

	t.Log("Building image from Dockerfile...")
	run(t, "podman", "build", "-t", image, repoRoot)

	t.Log("Starting container...")
	run(t, "podman", "run", "-d", "--name", container, "-p", port, image)
	waitHealthy(t, 30*time.Second)

	sid := initialize(t)

	t.Run("tools/list returns tools", func(t *testing.T) {
		resp := mcpCall(t, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
		if !strings.Contains(resp, "resolve_lexicon") {
			t.Fatalf("tools/list missing resolve_lexicon: %s", resp)
		}
		count := strings.Count(resp, `"name"`)
		t.Logf("tools/list returned %d tools", count)
	})
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").CombinedOutput()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	mod := strings.TrimSpace(string(out))
	if mod == "" {
		t.Fatal("not inside a Go module")
	}
	return filepath.Dir(mod)
}

func requireCmd(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not found, skipping integration test", name)
	}
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func cleanup(t *testing.T) {
	t.Helper()
	exec.Command("podman", "rm", "-f", container).Run()
	exec.Command("podman", "rmi", "-f", image).Run()
}

func waitHealthy(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	body := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"healthcheck","version":"0.1"}}}`
	for time.Now().Before(deadline) {
		resp, err := doMCP(body, "")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("container did not become healthy within timeout")
}

func initialize(t *testing.T) string {
	t.Helper()
	resp, err := doMCP(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`, "")
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid := resp.Header.Get("Mcp-Session-Id")
	resp.Body.Close()
	if sid == "" {
		t.Fatal("no Mcp-Session-Id")
	}
	doMCP(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, sid)
	return sid
}

func mcpCall(t *testing.T, sid, body string) string {
	t.Helper()
	resp, err := doMCP(body, sid)
	if err != nil {
		t.Fatalf("mcp call: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return string(raw)
}

func doMCP(body, sid string) (*http.Response, error) {
	req, _ := http.NewRequest("POST", addr+"/", bytes.NewReader([]byte(body)))
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
