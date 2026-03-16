package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/dpopsuev/lex/internal/protocol"
)

// New creates an HTTP reverse proxy that intercepts LLM API requests
// and injects enrichment rules into the system prompt.
func New(upstream string, svc *protocol.Service, opts protocol.EnrichOpts) (http.Handler, error) {
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, err
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
		},
	}

	return &enrichProxy{
		proxy:    proxy,
		svc:      svc,
		opts:     opts,
		upstream: target,
	}, nil
}

type enrichProxy struct {
	proxy    *httputil.ReverseProxy
	svc      *protocol.Service
	opts     protocol.EnrichOpts
	upstream *url.URL
}

func (p *enrichProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only intercept POST requests to chat/messages endpoints.
	if r.Method != http.MethodPost || !isChatEndpoint(r.URL.Path) {
		p.proxy.ServeHTTP(w, r)
		return
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		slog.Error("proxy: read body", "error", err)
		p.proxy.ServeHTTP(w, r)
		return
	}

	enriched := p.enrichBody(r.Context(), body)
	r.Body = io.NopCloser(bytes.NewReader(enriched))
	r.ContentLength = int64(len(enriched))
	p.proxy.ServeHTTP(w, r)
}

func (p *enrichProxy) enrichBody(ctx context.Context, body []byte) []byte {
	enrichment, err := p.svc.Enrich(ctx, "", p.opts)
	if err != nil || enrichment == "" {
		return body
	}
	return p.enrichBodyWith(body, enrichment)
}

// enrichBodyWith injects enrichment text into a request body.
// Supports Anthropic (system field) and OpenAI (messages array) formats.
func (p *enrichProxy) enrichBodyWith(body []byte, enrichment string) []byte {
	if enrichment == "" {
		return body
	}

	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		return body
	}

	// Anthropic format: { "system": "..." }
	if sys, ok := msg["system"]; ok {
		switch v := sys.(type) {
		case string:
			msg["system"] = enrichment + "\n\n" + v
		default:
			_ = v
			msg["system"] = enrichment
		}
		out, _ := json.Marshal(msg)
		return out
	}

	// OpenAI format: { "messages": [{"role": "system", "content": "..."}] }
	if messages, ok := msg["messages"].([]any); ok {
		injected := false
		for _, m := range messages {
			if msgMap, ok := m.(map[string]any); ok {
				if msgMap["role"] == "system" {
					if content, ok := msgMap["content"].(string); ok {
						msgMap["content"] = enrichment + "\n\n" + content
						injected = true
						break
					}
				}
			}
		}
		if !injected {
			sysMsg := map[string]any{"role": "system", "content": enrichment}
			msg["messages"] = append([]any{sysMsg}, messages...)
		}
		out, _ := json.Marshal(msg)
		return out
	}

	// Unknown format — inject "system" field (Anthropic-style).
	msg["system"] = enrichment
	out, _ := json.Marshal(msg)
	return out
}

func isChatEndpoint(path string) bool {
	return strings.HasSuffix(path, "/messages") ||
		strings.HasSuffix(path, "/chat/completions") ||
		path == "/v1/messages" ||
		path == "/v1/chat/completions"
}
