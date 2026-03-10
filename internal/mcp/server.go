package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dpopsuev/lex/internal/lexicon"
	"github.com/dpopsuev/lex/internal/protocol"
	"github.com/dpopsuev/lex/internal/registry"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func NewServer(reg *registry.Registry, workspaceRoots []string) *sdkmcp.Server {
	srv := sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "lex", Version: "0.2.0"},
		&sdkmcp.ServerOptions{
			Instructions: "Lex is a lexicon resolver for AI agents. " +
				"It reads .cursor/ rules and skills from local workspaces and merges them with remote lexicon repositories " +
				"using priority-based cascading. Use resolve_lexicon for smart routing (glob and label matching), " +
				"manage_lexicons to register and manage remote sources.",
		},
	)
	h := &handler{svc: protocol.New(reg, workspaceRoots)}

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "resolve_lexicon",
		Description: "Resolve effective rules and skills by merging local .cursor/ with remote lexicons. Higher priority wins on name conflicts. Supports path and label filters. Use source=local for workspace-only rules/skills, type=rules or type=skills to filter by artifact type.",
	}, noOut(h.handleResolveLexicon))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "inspect_lexicon",
		Description: "List all rules, skills, and templates from registered lexicon sources. If url is provided, filters to that source. If omitted, returns artifacts from all sources.",
	}, noOut(h.handleInspectLexicon))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "manage_lexicons",
		Description: "Manage lexicon sources. Actions: add (register remote repo), remove (delete source), enable/disable (toggle without removing), sync (re-fetch all), list (show sources with URLs, priorities, status).",
	}, noOut(h.handleManageLexicons))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "get_config",
		Description: "Return current global config (default_priority, cache_dir, enabled, labels).",
	}, noOut(h.handleGetConfig))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "set_config",
		Description: "Set a global config value. Keys: default_priority, cache_dir, enabled, labels (comma-separated).",
	}, noOut(h.handleSetConfig))

	return srv
}

type handler struct {
	svc *protocol.Service
}

// --- handlers ---

type pathInput struct {
	Path string `json:"path"`
}

func (h *handler) handleGetRules(ctx context.Context, _ *sdkmcp.CallToolRequest, in pathInput) (*sdkmcp.CallToolResult, any, error) {
	rules, err := h.svc.GetRules(ctx, in.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("read rules: %w", err)
	}
	return jsonResult(rules)
}

func (h *handler) handleGetSkills(ctx context.Context, _ *sdkmcp.CallToolRequest, in pathInput) (*sdkmcp.CallToolResult, any, error) {
	skills, err := h.svc.GetSkills(ctx, in.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("read skills: %w", err)
	}
	return jsonResult(skills)
}

type addLexiconInput struct {
	URL      string `json:"url"`
	Ref      string `json:"ref,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

func (h *handler) handleAddLexicon(ctx context.Context, _ *sdkmcp.CallToolRequest, in addLexiconInput) (*sdkmcp.CallToolResult, any, error) {
	if in.URL == "" {
		return nil, nil, fmt.Errorf("url is required")
	}
	src, err := h.svc.AddLexicon(ctx, in.URL, in.Ref, in.Priority)
	if err != nil {
		return nil, nil, fmt.Errorf("add lexicon: %w", err)
	}
	return jsonResult(src)
}

type emptyInput struct{}

func (h *handler) handleSyncLexicons(ctx context.Context, _ *sdkmcp.CallToolRequest, _ emptyInput) (*sdkmcp.CallToolResult, any, error) {
	n, err := h.svc.SyncLexicons(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("sync: %w", err)
	}
	return jsonResult(map[string]any{"synced": n})
}

func (h *handler) handleListLexicons(ctx context.Context, _ *sdkmcp.CallToolRequest, _ emptyInput) (*sdkmcp.CallToolResult, any, error) {
	sources, err := h.svc.ListSources(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list: %w", err)
	}
	return jsonResult(sources)
}

type resolveLexiconInput struct {
	Path       string   `json:"path,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Filter     string   `json:"filter,omitempty"`
	ActiveFile string   `json:"active_file,omitempty"`
	Context    []string `json:"context,omitempty"`
	Source     string   `json:"source,omitempty"` // local|remote|merged (default: merged)
	Type       string   `json:"type,omitempty"`   // rules|skills|all (default: all)
}

func (h *handler) handleResolveLexicon(ctx context.Context, _ *sdkmcp.CallToolRequest, in resolveLexiconInput) (*sdkmcp.CallToolResult, any, error) {
	source := strings.ToLower(strings.TrimSpace(in.Source))
	typ := strings.ToLower(strings.TrimSpace(in.Type))
	if typ == "" {
		typ = "all"
	}

	// Local-only: workspace .cursor/ rules/skills via GetRules/GetSkills
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

	// Merged or remote: full resolve, then post-filter by Type
	opts := lexicon.ResolveOpts{
		PathFilter: in.Filter,
		Labels:     in.Labels,
		ActiveFile: in.ActiveFile,
		Context:    in.Context,
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

type manageLexiconsInput struct {
	Action   string `json:"action"` // add|remove|enable|disable|sync|list
	URL      string `json:"url,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

func (h *handler) handleManageLexicons(ctx context.Context, _ *sdkmcp.CallToolRequest, in manageLexiconsInput) (*sdkmcp.CallToolResult, any, error) {
	switch in.Action {
	case "add":
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
	case "remove":
		if in.URL == "" {
			return nil, nil, fmt.Errorf("url is required for remove")
		}
		if err := h.svc.RemoveLexicon(ctx, in.URL); err != nil {
			return nil, nil, fmt.Errorf("remove lexicon: %w", err)
		}
		return jsonResult(map[string]string{"removed": in.URL})
	case "enable":
		if in.URL == "" {
			return nil, nil, fmt.Errorf("url is required for enable")
		}
		if err := h.svc.EnableSource(ctx, in.URL); err != nil {
			return nil, nil, fmt.Errorf("enable source: %w", err)
		}
		return jsonResult(map[string]string{"enabled": in.URL})
	case "disable":
		if in.URL == "" {
			return nil, nil, fmt.Errorf("url is required for disable")
		}
		if err := h.svc.DisableSource(ctx, in.URL); err != nil {
			return nil, nil, fmt.Errorf("disable source: %w", err)
		}
		return jsonResult(map[string]string{"disabled": in.URL})
	case "sync":
		n, err := h.svc.SyncLexicons(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("sync: %w", err)
		}
		return jsonResult(map[string]any{"synced": n})
	case "list":
		sources, err := h.svc.ListSources(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("list: %w", err)
		}
		return jsonResult(sources)
	default:
		return nil, nil, fmt.Errorf("unknown action %q; use: add, remove, enable, disable, sync, list", in.Action)
	}
}

type removeLexiconInput struct {
	URL string `json:"url"`
}

func (h *handler) handleRemoveLexicon(ctx context.Context, _ *sdkmcp.CallToolRequest, in removeLexiconInput) (*sdkmcp.CallToolResult, any, error) {
	if in.URL == "" {
		return nil, nil, fmt.Errorf("url is required")
	}
	if err := h.svc.RemoveLexicon(ctx, in.URL); err != nil {
		return nil, nil, fmt.Errorf("remove lexicon: %w", err)
	}
	return jsonResult(map[string]string{"removed": in.URL})
}

type inspectLexiconInput struct {
	URL string `json:"url,omitempty"`
}

func (h *handler) handleInspectLexicon(ctx context.Context, _ *sdkmcp.CallToolRequest, in inspectLexiconInput) (*sdkmcp.CallToolResult, any, error) {
	artifacts, err := h.svc.InspectLexicon(ctx, in.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect lexicon: %w", err)
	}
	return jsonResult(artifacts)
}

type cursorBridgeRuleInput struct {
	Path   string `json:"path,omitempty"`
	Global bool   `json:"global,omitempty"`
}

func (h *handler) handleCursorBridgeRule(ctx context.Context, _ *sdkmcp.CallToolRequest, in cursorBridgeRuleInput) (*sdkmcp.CallToolResult, any, error) {
	result, err := h.svc.InstallBridgeRule(ctx, in.Path, in.Global)
	if err != nil {
		return nil, nil, fmt.Errorf("install bridge rule: %w", err)
	}
	return jsonResult(result)
}

func (h *handler) handleGetConfig(ctx context.Context, _ *sdkmcp.CallToolRequest, _ emptyInput) (*sdkmcp.CallToolResult, any, error) {
	cfg, err := h.svc.GetConfig(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get config: %w", err)
	}
	return jsonResult(cfg)
}

type setConfigInput struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (h *handler) handleSetConfig(ctx context.Context, _ *sdkmcp.CallToolRequest, in setConfigInput) (*sdkmcp.CallToolResult, any, error) {
	if in.Key == "" {
		return nil, nil, fmt.Errorf("key is required")
	}
	if err := h.svc.SetConfig(ctx, in.Key, in.Value); err != nil {
		return nil, nil, fmt.Errorf("set config: %w", err)
	}
	return jsonResult(map[string]string{"ok": "config updated"})
}

type enableSourceInput struct {
	URL string `json:"url"`
}

func (h *handler) handleEnableSource(ctx context.Context, _ *sdkmcp.CallToolRequest, in enableSourceInput) (*sdkmcp.CallToolResult, any, error) {
	if in.URL == "" {
		return nil, nil, fmt.Errorf("url is required")
	}
	if err := h.svc.EnableSource(ctx, in.URL); err != nil {
		return nil, nil, fmt.Errorf("enable source: %w", err)
	}
	return jsonResult(map[string]string{"enabled": in.URL})
}

type disableSourceInput struct {
	URL string `json:"url"`
}

func (h *handler) handleDisableSource(ctx context.Context, _ *sdkmcp.CallToolRequest, in disableSourceInput) (*sdkmcp.CallToolResult, any, error) {
	if in.URL == "" {
		return nil, nil, fmt.Errorf("url is required")
	}
	if err := h.svc.DisableSource(ctx, in.URL); err != nil {
		return nil, nil, fmt.Errorf("disable source: %w", err)
	}
	return jsonResult(map[string]string{"disabled": in.URL})
}

// --- helpers ---

func jsonResult(data any) (*sdkmcp.CallToolResult, any, error) {
	b, _ := json.MarshalIndent(data, "", "  ")
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(b)}},
	}, nil, nil
}

func noOut[In any](h func(context.Context, *sdkmcp.CallToolRequest, In) (*sdkmcp.CallToolResult, any, error)) sdkmcp.ToolHandlerFor[In, any] {
	return h
}
