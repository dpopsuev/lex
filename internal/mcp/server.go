package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dpopsuev/lex/internal/lexicon"
	"github.com/dpopsuev/lex/internal/protocol"
	"github.com/dpopsuev/lex/internal/registry"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func NewServer(reg *registry.Registry, workspaceRoots []string) *sdkmcp.Server {
	srv := sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "lex", Version: "0.3.0"},
		&sdkmcp.ServerOptions{
			Instructions: "Lex is a lexicon resolver for AI agents. " +
				"It reads .cursor/ rules and skills from local workspaces and merges them with remote lexicon repositories " +
				"using priority-based cascading. Use resolve_lexicon for smart routing (glob and label matching), " +
				"add_lexicon to register remote sources, and get_rules/get_skills for direct access.",
		},
	)
	h := &handler{svc: protocol.New(reg, workspaceRoots)}

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "get_rules",
		Description: "Return all .cursor/rules/*.mdc rules for a workspace root, with frontmatter metadata and body content. Zero-config: reads existing Cursor rules directly.",
	}, noOut(h.handleGetRules))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "get_skills",
		Description: "Return all .cursor/skills/*/SKILL.md skills for a workspace root, with frontmatter metadata and body content. Zero-config: reads existing Cursor skills directly.",
	}, noOut(h.handleGetSkills))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "add_lexicon",
		Description: "Register a remote lexicon repository (git URL). Shallow-clones the repo and indexes all rules, templates, and skills found inside.",
	}, noOut(h.handleAddLexicon))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "sync_lexicons",
		Description: "Re-fetch all registered lexicon repositories to get the latest versions.",
	}, noOut(h.handleSyncLexicons))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "list_lexicons",
		Description: "List all registered lexicon sources with their URLs, priorities, and sync timestamps.",
	}, noOut(h.handleListLexicons))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "resolve_lexicon",
		Description: "Resolve effective rules and skills by merging local .cursor/ with remote lexicons. Higher priority wins on name conflicts. Supports path and label filters.",
	}, noOut(h.handleResolveLexicon))

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "remove_lexicon",
		Description: "Remove a registered lexicon source by URL. Deletes the source entry and prunes the cloned directory.",
	}, noOut(h.handleRemoveLexicon))

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
}

func (h *handler) handleResolveLexicon(ctx context.Context, _ *sdkmcp.CallToolRequest, in resolveLexiconInput) (*sdkmcp.CallToolResult, any, error) {
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
	return jsonResult(res)
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
