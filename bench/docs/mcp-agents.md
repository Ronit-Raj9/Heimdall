# MCP servers and memory on the agent

A task's services of role `mcp` are Model Context Protocol servers the agent should use. The engine starts them, routes a proxied one through the run's tool proxy (`http://toolproxy:9200/s/<id>/mcp`), and the command adapter tells the agent where each one is: in the agent's own config file, in `BENCH_MCP_SERVERS`, and in template variables. Hidden services are never listed.

## What every agent gets

| | Value |
|---|---|
| `BENCH_MCP_SERVERS` | `{"<id>": {"url": "<endpoint>"}}`, keys sorted |
| `{{mcp.servers_json}}` | the same JSON; `{}` when the run has no MCP server, so one config serves every task |
| `{{mcp.<id>.url}}` | one server's endpoint; an unknown id fails the plan like any unknown variable |

The variables work in `argv`, `env` and `files` of any command-adapter config. The endpoint is the service's URL plus its MCP path (default `/mcp`) when the URL does not already end with it.

A task with external HTTP APIs (`external:`, [`external-apis.md`](external-apis.md)) adds the same three for them: `BENCH_EXTERNAL_APIS` (`{"<id>": {"url": "http://toolproxy:9200/x/<id>"}}`, set only when the run has one), `{{external.apis_json}}` (`{}` when it has none) and `{{external.<id>.url}}`.

## Native config

```yaml
mcp:
  format: ""        # empty: the CLI's own format (table below)
  config_path: ""   # empty: the CLI's own file
```

| Agent (by `parser:`) | Format | File | Entry written |
|---|---|---|---|
| Codex (`codex`) | `codex_toml` | `$CODEX_HOME/config.toml` | `[mcp_servers.<id>]` with `url = "<endpoint>"` (streamable HTTP) |
| Claude Code (`claudecode`) | `claude_json` | `$CLAUDE_CONFIG_DIR/.claude.json` | `"mcpServers": {"<id>": {"type": "http", "url": ...}}` (user scope: no approval prompt) |
| OpenCode (`opencode`) | `opencode_json` | the file `OPENCODE_CONFIG` names, else `$XDG_CONFIG_HOME/opencode/opencode.json` | `"mcp": {"<id>": {"type": "remote", "url": ..., "enabled": true}}` |
| anything else | `env` | none | nothing; `BENCH_MCP_SERVERS` and the variables only |

`generic_json` writes `{"mcpServers": {"<id>": {"type": "http", "url": ...}}}` to `mcp.config_path`, which it requires. Any format can be forced with `mcp.format`, for example `claude_json` for another CLI that reads that shape.

When the agent config's `files:` already writes the target file, the servers are merged into it and nothing else changes:

- Codex: one `[mcp_servers.<id>]` table per server is appended after the existing text, so every existing line (`model_provider`, `[features]`, `plugins = false` and the rest) stays as written. A table already declared for the same id is an error.
- JSON formats: the file is parsed, the servers are added under their key, and the object is written back indented with sorted keys and no HTML escaping. Every other field is kept (numbers exactly, `{env:...}` substitutions as text). An id the file already defines is an error, never an override.

A run with no MCP server, including one whose services are all data or tool services, gets exactly the plan it had before (golden tests in `adapters/testdata/plans`).

### How the formats were checked

Against the CLIs in the bench's own images, with a trivial MCP server on a private network answering as `toolproxy:9200` (`adapters/integration_test.go`, `go test -tags integration ./adapters/`), and no model call:

| CLI | Command | Result |
|---|---|---|
| codex-cli 0.169.0 | `codex mcp list --json` | `tickets`, transport `streamable_http`, the proxy URL; `codex exec --strict-config` accepts the table and connects at start |
| Claude Code 2.1.325 | `claude mcp list`; stream-json `system/init` line | `Connected`; `mcp_servers: [{name: tickets, status: connected}]`, tool `mcp__tickets__ping` |
| OpenCode 1.19.5 | `opencode --pure mcp list` | `tickets connected` |

The paths match what each CLI's own `mcp add` writes: `codex mcp add --url` appends the same table to `config.toml`, and `claude mcp add --transport http --scope user` writes the same entry to `$CLAUDE_CONFIG_DIR/.claude.json`.

## Memory paths

`memory.paths` lists container folders kept between the sessions of a multi-session task (the environment gives each its own volume); `/memory` (`BENCH_MEMORY_DIR`) is always kept. The adapters set no default:

| CLI | Own memory | Default |
|---|---|---|
| Claude Code | Auto memory in `$CLAUDE_CONFIG_DIR/projects/<cwd with / as ->/memory` (for `/workspace`: `/home/agent/.claude/projects/-workspace/memory`); the folder moves with the `autoMemoryDirectory` setting. The shipped config turns it off (`CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`). | none. An arm that turns auto memory on lists the folder itself. |
| Codex | A `memories` feature, off in the shipped config (`[features] memories = false`); its storage location was not verified in codex-cli 0.169.0. | none |
| OpenCode | No memory feature. Sessions live in `~/.local/share/opencode`, but `opencode run` starts a new one each time. | none |

A memory path under the agent's home, next to files the CLI writes (its config folder), must stay writable by the agent; check that the environment creates its parent folders owned by the agent user before relying on it.
