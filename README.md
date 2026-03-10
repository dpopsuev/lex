<p align="center">
  <img src="assets/lex_logo.png" alt="Lex" width="200">
</p>

# Lex

Lexicon resolver for AI agents. Reads `.cursor/` rules and skills, merges them with remote lexicon repositories using priority-based cascading and smart routing — via CLI or MCP server.

## The Problem

AI agents need consistent rules, conventions, and skills across workspaces. Without a resolver, rules are scattered in local `.cursor/` directories, team conventions can't be shared, and there's no way to override or cascade. Lex solves this by providing a unified resolution layer.

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

**User:** I just cloned a new project. Link my private lexicon and show me what rules apply.

**Agent:** Adds the lexicon via `manage_lexicons` (action: add), then `resolve_lexicon` for the workspace path. Explains which rules matched from local `.cursor/` vs the remote lexicon, and how priority resolved any overlaps.

**User:** Show me only the security rules for Go files.

**Agent:** Uses `resolve_lexicon` with `labels: ["security"]` and `active_file: "internal/auth/handler.go"` (or another `.go` path). Smart routing returns only rules whose globs match `*.go` and whose labels intersect with `security`.

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

These show what an LLM agent sends over MCP. Copy-paste into any chat to see them in action.

**Resolve all rules and skills for a workspace:**

```json
{ "tool": "resolve_lexicon", "arguments": { "path": "/workspace/myproject" } }
```

**Get only rules (no skills):**

```json
{ "tool": "resolve_lexicon", "arguments": {
    "path": "/workspace/myproject", "type": "rules"
}}
```

**Get only local workspace skills:**

```json
{ "tool": "resolve_lexicon", "arguments": {
    "path": "/workspace/myproject", "source": "local", "type": "skills"
}}
```

**Resolve with label filtering:**

```json
{ "tool": "resolve_lexicon", "arguments": {
    "path": "/workspace/myproject", "labels": ["security", "go"]
}}
```

**Resolve for a specific file (glob-based routing):**

```json
{ "tool": "resolve_lexicon", "arguments": {
    "path": "/workspace/myproject", "active_file": "internal/auth/handler.go"
}}
```

**List all artifacts from registered sources:**

```json
{ "tool": "inspect_lexicon" }
```

**Register a remote lexicon repository:**

```json
{ "tool": "manage_lexicons", "arguments": {
    "action": "add", "url": "https://github.com/org/lexicon", "priority": 60
}}
```

**List registered sources:**

```json
{ "tool": "manage_lexicons", "arguments": { "action": "list" } }
```

**Disable a source without removing it:**

```json
{ "tool": "manage_lexicons", "arguments": {
    "action": "disable", "url": "https://github.com/org/lexicon"
}}
```

**Re-enable a disabled source:**

```json
{ "tool": "manage_lexicons", "arguments": {
    "action": "enable", "url": "https://github.com/org/lexicon"
}}
```

**Sync all sources (re-fetch):**

```json
{ "tool": "manage_lexicons", "arguments": { "action": "sync" } }
```

**Read global config:**

```json
{ "tool": "config", "arguments": { "action": "get" } }
```

**Set a config value:**

```json
{ "tool": "config", "arguments": { "action": "set", "key": "default_priority", "value": "40" } }
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
