package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dpopsuev/lex/internal/protocol"
	"github.com/dpopsuev/ordo/lexicon"
	"github.com/dpopsuev/ordo/registry"
	"github.com/dpopsuev/ordo/rule"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func NewServer(reg *registry.Registry, workspaceRoots []string, version string) *sdkmcp.Server {
	srv := sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "lex", Version: version},
		&sdkmcp.ServerOptions{
			Instructions: "Lex is a provider-agnostic prompt enrichment engine for AI agents. " +
				"It reads rules from multiple sources (.cursor/rules, CLAUDE.md, AGENTS.md, .github/copilot, remote repos) " +
				"and merges them using priority-based cascading with context-aware scoring. " +
				"Use the lexicon tool to resolve, inspect, and manage sources. " +
				"Pass language, files, and keywords params for context-aware resolution. " +
				"Use the config tool for global settings.",
		},
	)
	h := &handler{svc: protocol.New(reg, workspaceRoots)}

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "lexicon",
		Description: "Resolve effective rules and skills by merging local .cursor/ with remote lexicons. " +
			"Actions: resolve (merge local+remote with path/label filters), " +
			"search (find rules/skills by substring query across loaded sources), " +
			"inspect (list rules/skills/templates from registered sources), " +
			"add (register remote repo), remove (delete source), " +
			"enable/disable (toggle without removing), sync (re-fetch all), list (show sources).",
	}, noOut(h.handleLexicon))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "config",
		Description: "Get or set global configuration. " +
			"Actions: get (return current config), set (update a key). " +
			"Keys: default_priority, cache_dir, enabled, labels (comma-separated).",
	}, noOut(h.handleConfig))

	return srv
}

type handler struct {
	svc *protocol.Service
}

// --- lexicon tool (consolidated resolve + inspect + manage) ---

type lexiconInput struct {
	Action     string   `json:"action" jsonschema:"required,resolve | search | inspect | add | remove | enable | disable | sync | list"`
	Path       string   `json:"path,omitempty" jsonschema:"workspace path for resolve context"`
	Labels     []string `json:"labels,omitempty" jsonschema:"filter rules/skills by labels (resolve)"`
	Filter     string   `json:"filter,omitempty" jsonschema:"glob pattern to filter files (resolve)"`
	ActiveFile string   `json:"active_file,omitempty" jsonschema:"currently open file path for context-aware resolution"`
	Context    []string `json:"context,omitempty" jsonschema:"additional context strings for resolution"`
	Source     string   `json:"source,omitempty" jsonschema:"source filter: local, remote, or merged (default: merged)"`
	Type       string   `json:"type,omitempty" jsonschema:"artifact type filter: rules, skills, or all (default: all)"`
	URL        string   `json:"url,omitempty" jsonschema:"lexicon repository URL (add/remove/enable/disable/inspect)"`
	Ref        string   `json:"ref,omitempty" jsonschema:"git ref to pin (add)"`
	Priority   int      `json:"priority,omitempty" jsonschema:"source priority, higher wins on conflict (add)"`
	Query      string   `json:"query,omitempty" jsonschema:"substring to search for across loaded lexicons (search)"`
	Sources    []string `json:"sources,omitempty" jsonschema:"source names to search within (search, default: all)"`
	Language   string   `json:"language,omitempty" jsonschema:"programming language for context-aware scoring (resolve)"`
	Files      []string `json:"files,omitempty" jsonschema:"touched file paths for context-aware scoring (resolve)"`
	Keywords   []string `json:"keywords,omitempty" jsonschema:"domain keywords for context-aware scoring (resolve)"`
	Budget     int      `json:"budget,omitempty" jsonschema:"max tokens for returned rules, 0=unlimited (resolve)"`
}

func (h *handler) handleLexicon(ctx context.Context, req *sdkmcp.CallToolRequest, in lexiconInput) (*sdkmcp.CallToolResult, any, error) {
	switch in.Action {
	case "resolve":
		return h.doResolve(ctx, in)
	case "search":
		return h.doSearch(ctx, in)
	case "inspect":
		return h.doInspect(ctx, in)
	case "add":
		return h.doAdd(ctx, in)
	case "remove":
		return h.doRemove(ctx, in)
	case "enable":
		return h.doEnable(ctx, in)
	case "disable":
		return h.doDisable(ctx, in)
	case "sync":
		return h.doSync(ctx)
	case "list":
		return h.doList(ctx)
	default:
		return nil, nil, fmt.Errorf("unknown lexicon action %q (valid: resolve, search, inspect, add, remove, enable, disable, sync, list)", in.Action)
	}
}

func (h *handler) doSearch(ctx context.Context, in lexiconInput) (*sdkmcp.CallToolResult, any, error) {
	if in.Query == "" {
		return nil, nil, fmt.Errorf("query is required for search")
	}
	matches, err := h.svc.Search(ctx, in.Query, in.Sources)
	if err != nil {
		return nil, nil, fmt.Errorf("search: %w", err)
	}
	return jsonResult(map[string]any{"query": in.Query, "count": len(matches), "matches": matches})
}

func (h *handler) doResolve(ctx context.Context, in lexiconInput) (*sdkmcp.CallToolResult, any, error) {
	source := strings.ToLower(strings.TrimSpace(in.Source))
	typ := strings.ToLower(strings.TrimSpace(in.Type))
	if typ == "" {
		typ = "all"
	}

	if source == "local" {
		switch typ {
		case "rules":
			rules, err := h.svc.GetRules(ctx, in.Path)
			if err != nil {
				return nil, nil, fmt.Errorf("read rules: %w", err)
			}
			return jsonResult(rules)
		case "skills":
			skills, err := h.svc.GetSkills(ctx, in.Path)
			if err != nil {
				return nil, nil, fmt.Errorf("read skills: %w", err)
			}
			return jsonResult(skills)
		default:
			rules, err := h.svc.GetRules(ctx, in.Path)
			if err != nil {
				return nil, nil, fmt.Errorf("read rules: %w", err)
			}
			skills, err := h.svc.GetSkills(ctx, in.Path)
			if err != nil {
				return nil, nil, fmt.Errorf("read skills: %w", err)
			}
			return jsonResult(map[string]any{"rules": rules, "skills": skills})
		}
	}

	opts := lexicon.ResolveOpts{
		PathFilter: in.Filter,
		Labels:     in.Labels,
		ActiveFile: in.ActiveFile,
		Context:    in.Context,
		Signals: rule.ContextSignals{
			CWD:      in.Path,
			Language: in.Language,
			Files:    in.Files,
			Keywords: in.Keywords,
		},
		Budget: in.Budget,
	}
	res, err := h.svc.Resolve(ctx, in.Path, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve: %w", err)
	}

	if source == "remote" {
		res = filterBySource(res, false)
	}
	if typ == "rules" {
		return jsonResult(map[string]any{"rules": res.Rules})
	}
	if typ == "skills" {
		return jsonResult(map[string]any{"skills": res.Skills})
	}
	return jsonResult(res)
}

func (h *handler) doInspect(ctx context.Context, in lexiconInput) (*sdkmcp.CallToolResult, any, error) {
	artifacts, err := h.svc.InspectLexicon(ctx, in.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect lexicon: %w", err)
	}
	return jsonResult(artifacts)
}

func (h *handler) doAdd(ctx context.Context, in lexiconInput) (*sdkmcp.CallToolResult, any, error) {
	if in.URL == "" {
		return nil, nil, fmt.Errorf("url is required for add")
	}
	priority := in.Priority
	if priority == 0 {
		priority = 25
	}
	src, err := h.svc.AddLexicon(ctx, in.URL, in.Ref, priority)
	if err != nil {
		return nil, nil, fmt.Errorf("add lexicon: %w", err)
	}
	return jsonResult(src)
}

func (h *handler) doRemove(ctx context.Context, in lexiconInput) (*sdkmcp.CallToolResult, any, error) {
	if in.URL == "" {
		return nil, nil, fmt.Errorf("url is required for remove")
	}
	if err := h.svc.RemoveLexicon(ctx, in.URL); err != nil {
		return nil, nil, fmt.Errorf("remove lexicon: %w", err)
	}
	return jsonResult(map[string]string{"removed": in.URL})
}

func (h *handler) doEnable(ctx context.Context, in lexiconInput) (*sdkmcp.CallToolResult, any, error) {
	if in.URL == "" {
		return nil, nil, fmt.Errorf("url is required for enable")
	}
	if err := h.svc.EnableSource(ctx, in.URL); err != nil {
		return nil, nil, fmt.Errorf("enable source: %w", err)
	}
	return jsonResult(map[string]string{"enabled": in.URL})
}

func (h *handler) doDisable(ctx context.Context, in lexiconInput) (*sdkmcp.CallToolResult, any, error) {
	if in.URL == "" {
		return nil, nil, fmt.Errorf("url is required for disable")
	}
	if err := h.svc.DisableSource(ctx, in.URL); err != nil {
		return nil, nil, fmt.Errorf("disable source: %w", err)
	}
	return jsonResult(map[string]string{"disabled": in.URL})
}

func (h *handler) doSync(ctx context.Context) (*sdkmcp.CallToolResult, any, error) {
	n, err := h.svc.SyncLexicons(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("sync: %w", err)
	}
	return jsonResult(map[string]any{"synced": n})
}

func (h *handler) doList(ctx context.Context) (*sdkmcp.CallToolResult, any, error) {
	sources, err := h.svc.ListSources(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list: %w", err)
	}
	return jsonResult(sources)
}

func filterBySource(res *lexicon.Resolution, keepLocal bool) *lexicon.Resolution {
	out := &lexicon.Resolution{}
	for _, r := range res.Rules {
		if (keepLocal && r.Source == "local") || (!keepLocal && r.Source != "local") {
			out.Rules = append(out.Rules, r)
		}
	}
	for _, s := range res.Skills {
		if (keepLocal && s.Source == "local") || (!keepLocal && s.Source != "local") {
			out.Skills = append(out.Skills, s)
		}
	}
	return out
}

// --- config tool ---

type configInput struct {
	Action string `json:"action" jsonschema:"required,get | set"`
	Key    string `json:"key,omitempty" jsonschema:"configuration key to update (set)"`
	Value  string `json:"value,omitempty" jsonschema:"new value for the key (set)"`
}

func (h *handler) handleConfig(ctx context.Context, req *sdkmcp.CallToolRequest, in configInput) (*sdkmcp.CallToolResult, any, error) {
	switch in.Action {
	case "get":
		cfg, err := h.svc.GetConfig(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("get config: %w", err)
		}
		return jsonResult(cfg)
	case "set":
		if in.Key == "" {
			return nil, nil, fmt.Errorf("key is required")
		}
		if err := h.svc.SetConfig(ctx, in.Key, in.Value); err != nil {
			return nil, nil, fmt.Errorf("set config: %w", err)
		}
		return jsonResult(map[string]string{"ok": "config updated"})
	default:
		return nil, nil, fmt.Errorf("unknown config action %q (valid: get, set)", in.Action)
	}
}

// --- helpers ---

func jsonResult(data any) (*sdkmcp.CallToolResult, any, error) {
	b, _ := json.MarshalIndent(data, "", "  ")
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(b)}},
	}, nil, nil
}

func noOut[In any](h func(context.Context, *sdkmcp.CallToolRequest, In) (*sdkmcp.CallToolResult, any, error)) sdkmcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest, in In) (*sdkmcp.CallToolResult, any, error) {
		tool := ""
		if req != nil {
			tool = req.Params.Name
		}
		start := time.Now()
		result, out, err := h(ctx, req, in)
		elapsed := time.Since(start)
		if err != nil {
			slog.Error("tool call failed", "tool", tool, "elapsed", elapsed, "error", err)
		} else {
			slog.Debug("tool call", "tool", tool, "elapsed", elapsed)
		}
		return result, out, err
	}
}
