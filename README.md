# Lex

Lexicon resolver for AI agents. Reads `.cursor/` rules and skills, merges them with remote lexicon repositories using priority-based cascading and smart routing — via CLI or MCP server.

## Quickstart (container)

```bash
docker run -d --name lex \
  -p 8082:8082 \
  -v lex-data:/data \
  quay.io/dpopsuev/lex
```

## Quickstart (binary)

```bash
go install github.com/dpopsuev/lex/cmd/lex@latest
lex serve                             # stdio (Cursor/Claude)
lex serve --transport http            # HTTP on :8082
```

## Cursor MCP configuration

### stdio (local binary)

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

### HTTP (container)

```json
{
  "mcpServers": {
    "lex": {
      "url": "http://localhost:8082/"
    }
  }
}
```

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `LEX_ROOT` | `~/.lex` | Lexicon storage root |
| `LEX_TRANSPORT` | `stdio` | Transport: `stdio`, `http` |
| `LEX_ADDR` | `:8082` | Listen address (http only) |

## MCP tools

| Tool | Description |
|---|---|
| `resolve_lexicon` | Resolve rules + skills from local and remote lexicons. Use `source=local\|remote\|merged`, `type=rules\|skills\|all`. |
| `inspect_lexicon` | List rules, skills, and templates from registered sources. |
| `manage_lexicons` | Manage sources: `action=add\|remove\|enable\|disable\|sync\|list`. |
| `get_config` | Return current global configuration. |
| `set_config` | Set a global configuration value. |

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
{ "tool": "get_config" }
```

**Set a config value:**

```json
{ "tool": "set_config", "arguments": { "key": "default_priority", "value": "40" } }
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

## Smart routing

Lex supports context-aware rule resolution:

- **Glob matching**: rules with `globs` patterns are only returned when `--file` matches
- **Label matching**: rules with `labels` are only returned when `--context` keywords intersect
- **Always-apply**: rules marked `always_apply: true` are always included

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
