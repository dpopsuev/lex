package lexicon

import (
	"context"

	"github.com/dpopsuev/lex/internal/registry"
)

func Add(ctx context.Context, reg *registry.Registry, gitURL, ref string, priority int) (*registry.Source, error) {
	return reg.Add(ctx, gitURL, ref, priority)
}

func Sync(ctx context.Context, reg *registry.Registry) (int, error) {
	return reg.Sync(ctx)
}

func ListSources(_ context.Context, reg *registry.Registry) ([]registry.Source, error) {
	return reg.Load()
}

func Remove(ctx context.Context, reg *registry.Registry, url string) error {
	return reg.Remove(ctx, url)
}
