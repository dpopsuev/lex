<p align="center">
  <img src="assets/lex_logo.png" alt="Lex" width="200">
</p>

# Lex

Lexicon resolver for AI agents. Reads `.cursor/` rules and skills, merges them with remote lexicon repositories using priority-based cascading and smart routing — via CLI or MCP server.

## Quick Start

### Container (recommended)

```bash
docker run -d --name lex \
  -p 8082:8082 \
  -v lex-data:/data \
  quay.io/dpopsuev/lex
```

### Binary

```bash
go install github.com/dpopsuev/lex/cmd/lex@latest
lex serve                             # stdio (Cursor/Claude)
lex serve --transport http            # HTTP on :8082
```

### MCP Configuration

**stdio (local binary):**

```json
{
  "mcpServers": {
    "lex": {
      "command": "lex",
      "args": ["serve"]
    }
  }
}
```

**HTTP (container):**

```json
{
  "mcpServers": {
    "lex": {
      "url": "http://localhost:8082/"
    }
  }
}
```

## Workflow

> **You:** Which developer best practices do we have loaded, and from which lexicon?
>
> **Agent:** *(calls `resolve_lexicon`)* You have 12 rules active. 4 come from your local `.cursor/rules/` (priority 100) — project standards, testing methodology, deterministic-first, and security analysis. The other 8 come from your remote lexicon at `github.com/dpopsuev/lexicon` (priority 50) — Go conventions, reviewability-first, commit standards, and more. Local rules win on any overlap.

> **You:** Add my team's shared lexicon so every project picks it up.
>
> **Agent:** *(calls `manage_lexicons` with action `add`, url `https://github.com/myorg/lexicon`, priority 60)* Done. It's registered at priority 60, so it overrides the default lexicon (25) but your local `.cursor/` rules (100) still take precedence. Run `resolve_lexicon` on any workspace to see the merged result.

> **You:** I'm editing `internal/auth/handler.go` — are there security-specific rules I should know about?
>
> **Agent:** *(calls `resolve_lexicon` with active_file and labels `["security"]`)* Yes, 2 rules matched: `security-analysis.mdc` from your local workspace (OWASP Top 10 checklist for every trust-boundary change) and `secure-defaults.mdc` from your remote lexicon (input validation, no hardcoded secrets). Both apply to `*.go` files.

> **You:** That remote lexicon is stale. Sync it.
>
> **Agent:** *(calls `manage_lexicons` with action `sync`)* All sources re-fetched. The remote lexicon pulled 3 new rules since last sync.

## The Problem

AI agents need consistent rules, conventions, and skills across workspaces. Without a resolver, rules are scattered in local `.cursor/` directories, team conventions can't be shared, and there's no way to override or cascade. Lex solves this by providing a unified resolution layer.

## Core Concepts

| Concept | What it is |
|---------|------------|
| Rule | A `.md` file in `.cursor/rules/`. Contains AI instructions. |
| Skill | A directory with `SKILL.md` in `.cursor/skills/`. Extends agent capabilities. |
| Lexicon | A remote Git repository containing rules, skills, and templates. |
| Priority | Higher number wins. Local workspace rules default to 100, remote defaults to 25. |
| Smart Routing | Glob and label matching to serve only relevant rules per file/context. |
| Always-Apply | Rules marked `always_apply: true` bypass routing filters. |

## Architecture

Lex has three layers: **CLI** (`cmd/lex`), **MCP server** (`internal/mcp`), and **protocol/registry** (`internal/protocol`, `internal/registry`, `internal/lexicon`). The registry manages source configuration in `~/.lex/repos.d/`. Resolution merges local `.cursor/` artifacts with remote lexicon clones.

## Smart Routing

Lex supports context-aware rule resolution:

- **Glob matching**: rules with `globs` patterns are only returned when `--file` matches
- **Label matching**: rules with `labels` are only returned when `--context` keywords intersect
- **Always-apply**: rules marked `always_apply: true` are always included

**Example:**

```bash
lex add https://github.com/myorg/lexicon --priority 60
lex resolve --file internal/auth/handler.go --context security
```

Resolution order: local (pri 100) > remote (pri 60) > defaults (pri 25).

Configure routing in your `lexicon.yaml`:

```yaml
routing:
  - match:
      labels: [security]
    globs: ["*.go", "*.py", "*.rs"]
  - match:
      labels: [testing]
    always_apply: true
```

## MCP Tools

| Tool | Description |
|------|-------------|
| `resolve_lexicon` | Resolve effective rules and skills. Supports path, labels, source, type, active_file, context filters. |
| `inspect_lexicon` | List all artifacts from registered lexicon sources. |
| `manage_lexicons` | Manage sources: add, remove, enable, disable, sync, list. |
| `config` | Get or set global configuration. Actions: `get`, `set`. Keys: default_priority, cache_dir, enabled, labels. |

## LLM Chatbox Examples

Quick reference for what the agent sends over MCP. The Workflow section above shows full conversations.

```json
// What rules/skills apply to this workspace?
{ "tool": "resolve_lexicon", "arguments": { "path": "/workspace/myproject" } }

// Only security rules for Go files
{ "tool": "resolve_lexicon", "arguments": { "path": "/workspace/myproject", "labels": ["security"], "active_file": "internal/auth/handler.go" } }

// Add a remote lexicon
{ "tool": "manage_lexicons", "arguments": { "action": "add", "url": "https://github.com/myorg/lexicon", "priority": 60 } }

// List registered sources
{ "tool": "manage_lexicons", "arguments": { "action": "list" } }

// Sync all sources (re-fetch)
{ "tool": "manage_lexicons", "arguments": { "action": "sync" } }

// List everything a source provides
{ "tool": "inspect_lexicon" }
```

## Configuration

Lex uses a DNF-inspired layered configuration system:

### Global config: `~/.lex/config.yaml`

```yaml
default_priority: 50
cache_dir: ~/.lex/cache
enabled: true
```

### Per-source config: `~/.lex/repos.d/*.yaml`

Each registered lexicon source gets its own config file:

```yaml
url: https://github.com/org/lexicon
enabled: true
priority: 50
ref: main
labels:
  - production
```

### Source management

```bash
lex add https://github.com/org/lexicon    # register a source
lex disable https://github.com/org/lexicon # disable without removing
lex enable https://github.com/org/lexicon  # re-enable
lex config                                 # show global config
lex config set default_priority 40         # set a config value
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LEX_ROOT` | `~/.lex` | Lexicon storage root |
| `LEX_TRANSPORT` | `stdio` | Transport: `stdio`, `http` |
| `LEX_ADDR` | `:8082` | Listen address (http only) |
| `LEX_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error`. JSON to stderr. |

## License

MIT
