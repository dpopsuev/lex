package cursor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteBridgeRule_WorkspaceMode(t *testing.T) {
	dir := t.TempDir()

	result, err := WriteBridgeRule(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created {
		t.Error("expected Created=true on first write")
	}
	expected := filepath.Join(dir, ".cursor", "rules", "lex-bridge.mdc")
	if result.Path != expected {
		t.Errorf("expected path %s, got %s", expected, result.Path)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "resolve_lexicon") {
		t.Error("bridge rule should mention resolve_lexicon")
	}
	if !strings.Contains(string(data), "alwaysApply: true") {
		t.Error("bridge rule should have alwaysApply: true")
	}
}

func TestWriteBridgeRule_GlobalMode(t *testing.T) {
	dir := t.TempDir()

	result, err := WriteBridgeRule(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created {
		t.Error("expected Created=true")
	}
	expected := filepath.Join(dir, "lex-bridge.mdc")
	if result.Path != expected {
		t.Errorf("expected path %s, got %s", expected, result.Path)
	}
}

func TestWriteBridgeRule_Idempotent(t *testing.T) {
	dir := t.TempDir()

	first, err := WriteBridgeRule(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created {
		t.Error("first call should create")
	}

	second, err := WriteBridgeRule(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created {
		t.Error("second call should report already exists")
	}
	if second.Path != first.Path {
		t.Error("paths should match")
	}
}
