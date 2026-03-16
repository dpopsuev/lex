package rule

// TriggerType classifies how a rule should match against context.
type TriggerType string

const (
	TriggerFileGlob TriggerType = "file_glob"
	TriggerLanguage TriggerType = "language"
	TriggerKeyword  TriggerType = "keyword"
	TriggerAlways   TriggerType = "always"
)

// Trigger pairs a type with a match pattern.
type Trigger struct {
	Type    TriggerType
	Pattern string
}

// Rule is the canonical enrichment unit. All source adapters convert
// their native format into this type before resolution.
type Rule struct {
	Name     string
	Kind     string // "rule" or "skill"
	Source   string // "local", source URL, adapter name
	Adapter  string // "cursor", "claude", "codex", "copilot", "remote"
	Content  string
	Scope    string // "project" or "global"
	Triggers []Trigger
	Priority int
	Labels   []string
	Globs    []string // original globs for backward compat
}

// TokenEstimate returns a rough token count for budget tracking.
func (r *Rule) TokenEstimate() int {
	n := len(r.Content) / 4
	if n == 0 && len(r.Content) > 0 {
		n = 1
	}
	return n
}

// ContextSignals describes the current workspace context used for scoring.
type ContextSignals struct {
	CWD      string
	Language string
	Files    []string
	Keywords []string
}

// ResolveOpts controls resolution behavior.
type ResolveOpts struct {
	Signals ContextSignals
	Budget  int // max total tokens; 0 = unlimited
}
