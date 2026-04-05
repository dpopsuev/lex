package cursor

import (
	"os"
	"path/filepath"
)

const bridgeRuleFilename = "lex-bridge.mdc"

const bridgeRuleContent = `---
description: Bridge Lex remote lexicon rules into the agent context at session start
alwaysApply: true
---

# Lex Bridge

At session start, if Lex MCP tools are available, call ` + "`resolve_lexicon`" + ` to load applicable remote rules and skills for the current workspace. This bridges the gap between Cursor's native ` + "`.cursor/rules/`" + ` injection (push-based) and Lex's MCP-based rule resolution (pull-based).
`

// BridgeRuleResult describes the outcome of installing the bridge rule.
type BridgeRuleResult struct {
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

// WriteBridgeRule writes the lex-bridge.mdc file into the given directory's
// .cursor/rules/ subdirectory. If global is true, dir is treated as the
// direct target (e.g. ~/.cursor/rules); otherwise .cursor/rules is appended.
// Returns the file path and whether it was newly created.
func WriteBridgeRule(dir string, global bool) (*BridgeRuleResult, error) {
	rulesDir := dir
	if !global {
		rulesDir = filepath.Join(dir, ".cursor", "rules")
	}
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		return nil, err
	}

	target := filepath.Join(rulesDir, bridgeRuleFilename)
	if _, err := os.Stat(target); err == nil {
		return &BridgeRuleResult{Path: target, Created: false}, nil
	}

	if err := os.WriteFile(target, []byte(bridgeRuleContent), 0o644); err != nil { //nolint:gosec // G306: bridge rule file must be world-readable for Cursor IDE
		return nil, err
	}
	return &BridgeRuleResult{Path: target, Created: true}, nil
}

// GlobalCursorRulesDir returns ~/.cursor/rules.
func GlobalCursorRulesDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cursor", "rules")
}
