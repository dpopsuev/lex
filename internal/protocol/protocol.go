package protocol

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dpopsuev/lex/internal/config"
	"github.com/dpopsuev/lex/internal/cursor"
	"github.com/dpopsuev/ordo/adapter"
	"github.com/dpopsuev/ordo/lexicon"
	"github.com/dpopsuev/ordo/registry"
	"github.com/dpopsuev/ordo/rule"
)

// Service encapsulates all Lex business logic.
// Both CLI and MCP are thin wrappers around this.
type Service struct {
	reg        *registry.Registry
	workspaces []string
}

func New(reg *registry.Registry, workspaces []string) *Service {
	return &Service{reg: reg, workspaces: workspaces}
}

func (s *Service) GetRules(_ context.Context, path string) ([]cursor.Rule, error) {
	return cursor.ReadRules(s.resolvePath(path))
}

func (s *Service) GetSkills(_ context.Context, path string) ([]cursor.Skill, error) {
	return cursor.ReadSkills(s.resolvePath(path))
}

func (s *Service) AddLexicon(ctx context.Context, url, ref string, priority int) (*registry.Source, error) {
	if priority == 0 {
		priority = 25
	}
	return lexicon.Add(ctx, s.reg, url, ref, priority)
}

func (s *Service) SyncLexicons(ctx context.Context) (int, error) {
	return lexicon.Sync(ctx, s.reg)
}

func (s *Service) ListSources(ctx context.Context) ([]registry.Source, error) {
	return lexicon.ListSources(ctx, s.reg)
}

func (s *Service) RemoveLexicon(ctx context.Context, url string) error {
	return lexicon.Remove(ctx, s.reg, url)
}

func (s *Service) EnableSource(_ context.Context, url string) error {
	return s.reg.EnableSource(url)
}

func (s *Service) DisableSource(_ context.Context, url string) error {
	return s.reg.DisableSource(url)
}

func (s *Service) SetSourcePriority(_ context.Context, url string, priority int) error {
	return s.reg.SetSourcePriority(url, priority)
}

func (s *Service) GetConfig(_ context.Context) (*config.Config, error) {
	return config.Load(s.reg.Root())
}

func (s *Service) SetConfig(_ context.Context, key, value string) error {
	cfg, err := config.Load(s.reg.Root())
	if err != nil {
		return err
	}
	if err := cfg.SetConfig(key, value); err != nil {
		return err
	}
	return cfg.Save(s.reg.Root())
}

func (s *Service) Resolve(ctx context.Context, path string, opts lexicon.ResolveOpts) (*lexicon.Resolution, error) {
	return lexicon.Resolve(ctx, s.reg, s.resolvePath(path), opts)
}

// InspectLexicon returns discovered artifacts (rules, skills, templates)
// from registered lexicon sources. If url is empty, all sources are included.
func (s *Service) InspectLexicon(_ context.Context, url string) ([]registry.Artifact, error) {
	sources, err := s.reg.Load()
	if err != nil {
		return nil, err
	}
	var artifacts []registry.Artifact
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		if url != "" && src.URL != url {
			continue
		}
		artifacts = append(artifacts, registry.DiscoverArtifacts(src.LocalPath, src.URL, src.Priority)...)
	}
	if url != "" && len(artifacts) == 0 {
		return nil, fmt.Errorf("lexicon source not registered: %s", url)
	}
	return artifacts, nil
}

// InstallBridgeRule writes the lex-bridge.mdc Cursor rule.
// If global is true, writes to ~/.cursor/rules/. Otherwise writes to <path>/.cursor/rules/.
func (s *Service) InstallBridgeRule(_ context.Context, path string, global bool) (*cursor.BridgeRuleResult, error) {
	if global {
		return cursor.WriteBridgeRule(cursor.GlobalCursorRulesDir(), true)
	}
	return cursor.WriteBridgeRule(s.resolvePath(path), false)
}

func (s *Service) Search(ctx context.Context, query string, sources []string) ([]lexicon.Match, error) {
	return lexicon.Search(ctx, s.reg, s.resolvePath(""), query, sources)
}

// EnrichOpts controls enrichment output.
type EnrichOpts struct {
	Format   string // "text", "gemini", "agents-md"
	Language string
	Files    []string
	Keywords []string
	Budget   int
}

// Enrich resolves rules for the given workspace and formats them for hook output.
func (s *Service) Enrich(ctx context.Context, cwd string, opts EnrichOpts) (string, error) {
	root := s.resolvePath(cwd)

	// Collect rules from all local adapters.
	localRules, _ := adapter.DetectAndLoad(root)

	// Collect rules from remote sources.
	sources, _ := s.reg.Load()
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		cfg, _ := registry.LoadLexiconConfig(src.LocalPath)
		effectivePriority := src.Priority
		if cfg != nil && cfg.Defaults.Priority > 0 {
			effectivePriority = cfg.Defaults.Priority
		}
		artifacts := registry.DiscoverArtifacts(src.LocalPath, src.URL, effectivePriority)
		for _, a := range artifacts {
			body := readArtifactBody(a.Path)
			rr := rule.Rule{
				Name:     a.Name,
				Kind:     a.Type,
				Source:   a.Source,
				Adapter:  "remote",
				Content:  body,
				Scope:    "global",
				Priority: a.Priority,
				Labels:   a.Labels,
			}
			for _, l := range a.Labels {
				rr.Triggers = append(rr.Triggers, rule.Trigger{Type: rule.TriggerKeyword, Pattern: l})
			}
			localRules = append(localRules, rr)
		}
	}

	// Resolve with scoring and budget.
	budget := opts.Budget
	if budget == 0 {
		budget = 2000
	}
	resolved := rule.Resolve(localRules, rule.ResolveOpts{
		Signals: rule.ContextSignals{
			CWD:      root,
			Language: opts.Language,
			Files:    opts.Files,
			Keywords: opts.Keywords,
		},
		Budget: budget,
	})

	return formatEnrichment(resolved, opts.Format), nil
}

func readArtifactBody(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// Strip frontmatter if present.
	s := string(data)
	if strings.HasPrefix(s, "---") {
		if idx := strings.Index(s[3:], "---"); idx >= 0 {
			s = s[idx+6:]
		}
	}
	return strings.TrimSpace(s)
}

func formatEnrichment(rules []rule.Rule, format string) string {
	if len(rules) == 0 {
		return ""
	}

	switch format {
	case "agents-md":
		var sb strings.Builder
		sb.WriteString("# Project Rules\n\n")
		for _, r := range rules {
			sb.WriteString("## ")
			sb.WriteString(r.Name)
			sb.WriteString("\n\n")
			sb.WriteString(r.Content)
			sb.WriteString("\n\n")
		}
		return strings.TrimSpace(sb.String())

	case "gemini":
		var sb strings.Builder
		for _, r := range rules {
			sb.WriteString(r.Content)
			sb.WriteString("\n\n")
		}
		return fmt.Sprintf(`{"content": %q}`, strings.TrimSpace(sb.String()))

	default: // "text"
		var parts []string
		for _, r := range rules {
			parts = append(parts, r.Content)
		}
		return strings.Join(parts, "\n\n---\n\n")
	}
}

func (s *Service) resolvePath(path string) string {
	if path != "" {
		return path
	}
	if len(s.workspaces) > 0 {
		return s.workspaces[0]
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}
