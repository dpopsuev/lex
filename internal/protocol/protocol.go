package protocol

import (
	"context"
	"os"

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

func (s *Service) Resolve(ctx context.Context, path string, opts lexicon.ResolveOpts) (*lexicon.Resolution, error) {
	return lexicon.Resolve(ctx, s.reg, s.resolvePath(path), opts)
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
