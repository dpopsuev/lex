package adapter

import "github.com/dpopsuev/lex/internal/rule"

// SourceAdapter reads rules from a provider-specific format.
type SourceAdapter interface {
	Name() string
	Detect(root string) bool
	Load(root string) ([]rule.Rule, error)
}

var adapters []SourceAdapter

// Register adds an adapter to the global registry.
func Register(a SourceAdapter) { adapters = append(adapters, a) }

// All returns all registered adapters.
func All() []SourceAdapter { return adapters }

// DetectAndLoad runs Detect on each registered adapter and loads rules
// from all that match.
func DetectAndLoad(root string) ([]rule.Rule, error) {
	var all []rule.Rule
	for _, a := range adapters {
		if !a.Detect(root) {
			continue
		}
		rules, err := a.Load(root)
		if err != nil {
			return nil, err
		}
		all = append(all, rules...)
	}
	return all, nil
}
