package proxy

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestIsChatEndpoint(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/v1/messages", true},
		{"/v1/chat/completions", true},
		{"/api/messages", true},
		{"/health", false},
		{"/v1/models", false},
	}
	for _, tt := range tests {
		if got := isChatEndpoint(tt.path); got != tt.want {
			t.Errorf("isChatEndpoint(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestEnrichBody_Anthropic(t *testing.T) {
	p := &enrichProxy{}
	body := []byte(`{"model":"claude-3","system":"Be helpful.","messages":[]}`)

	enriched := p.enrichBodyWith(body, "INJECTED RULES")

	var msg map[string]any
	if err := json.Unmarshal(enriched, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sys, ok := msg["system"].(string)
	if !ok {
		t.Fatal("system field missing")
	}
	if sys != "INJECTED RULES\n\nBe helpful." {
		t.Fatalf("unexpected system: %q", sys)
	}
}

func TestEnrichBody_OpenAI(t *testing.T) {
	p := &enrichProxy{}
	body := []byte(`{"model":"gpt-4","messages":[{"role":"system","content":"Be helpful."},{"role":"user","content":"Hi"}]}`)

	enriched := p.enrichBodyWith(body, "INJECTED RULES")

	var msg map[string]any
	if err := json.Unmarshal(enriched, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	messages := msg["messages"].([]any)
	sysMsg := messages[0].(map[string]any)
	if sysMsg["content"] != "INJECTED RULES\n\nBe helpful." {
		t.Fatalf("unexpected system content: %q", sysMsg["content"])
	}
}

func TestEnrichBody_OpenAI_NoSystemMessage(t *testing.T) {
	p := &enrichProxy{}
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hi"}]}`)

	enriched := p.enrichBodyWith(body, "INJECTED RULES")

	var msg map[string]any
	if err := json.Unmarshal(enriched, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	messages := msg["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages (injected system + user), got %d", len(messages))
	}
	sysMsg := messages[0].(map[string]any)
	if sysMsg["role"] != "system" || sysMsg["content"] != "INJECTED RULES" {
		t.Fatalf("unexpected injected system message: %v", sysMsg)
	}
}

func TestEnrichBody_EmptyEnrichment(t *testing.T) {
	p := &enrichProxy{}
	body := []byte(`{"model":"claude-3","system":"Be helpful."}`)

	enriched := p.enrichBodyWith(body, "")
	if !bytes.Equal(enriched, body) {
		t.Fatalf("empty enrichment should return body unchanged")
	}
}
