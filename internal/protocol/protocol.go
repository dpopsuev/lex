package protocol

import (
	"context"
	"fmt"
	"os"

	"github.com/dpopsuev/lex/internal/config"
	"github.com/dpopsuev/lex/internal/cursor"
	"github.com/dpopsuev/lex/internal/lexicon"
	"github.com/dpopsuev/lex/internal/registry"
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
