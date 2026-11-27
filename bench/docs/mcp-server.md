# Heimdall MCP server

`bifrost-bench mcp serve` runs a Model Context Protocol server on stdin and stdout, so an assistant (Claude Code, Claude Desktop, Cursor, any MCP client) can benchmark your agents for you: list agents and benchmarks, estimate an evaluation, run it, read the report card, open run traces and explain failures. Every tool calls the public API v1 (`docs/api-v1.md`) of a Heimdall server with your API token; the server keeps no state of its own.

```sh
export HEIMDALL_URL=https://heimdall.example.com
export HEIMDALL_TOKEN=hm_...          # Settings > Access tokens; scope "Run evaluations" for run_evaluation
bifrost-bench mcp serve
```

| Flag | Default | Meaning |
|---|---|---|
| `-url` | `$HEIMDALL_URL`, else the server `heimdall login` stored, else `http://127.0.0.1:8090` | Heimdall server root |
| `-token-env` | `HEIMDALL_TOKEN` | environment variable holding the token (an `hm_` org token, or the local write token); when it is empty, the token `heimdall login` stored for the server |
| `-timeout` | `60s` | per-request timeout |

Claude Code: `claude mcp add heimdall -e HEIMDALL_URL=https://heimdall.example.com -e HEIMDALL_TOKEN=hm_... -- bifrost-bench mcp serve`. Other clients take the same command and environment in their MCP server settings.

With `heimdall login` done once on the machine, no token goes into the editor's settings at all: Integrations > IDE in the web UI has **Add to Cursor** (a `cursor://` install link holding only the command and `HEIMDALL_URL`), **Add to Claude Code** (`claude mcp add heimdall -e HEIMDALL_URL=https://heimdall.example.com -- heimdall mcp serve`) and the `.vscode/mcp.json` body. The server runs locally on purpose: it reads the workspace the editor has open.

## Remote endpoint (no install)

A hosted Heimdall also answers MCP itself at `/mcp` (Streamable HTTP), so nothing has to be installed:

```sh
claude mcp add --transport http heimdall https://heimdall.example.com/mcp \
  --header "Authorization: Bearer $HEIMDALL_TOKEN"
```

- It serves the same tools as `mcp serve`. Each tool's API call runs inside the server as your token, through the same policy, rate limits and audit as a direct API call, so the token's role and scope decide what it may do.
- An org API token (`hm_...`) is required on every request: no token answers 401 with `WWW-Authenticate: Bearer`, a runner token 403. Browser requests from another origin are refused.
- It is stateless: POST one JSON-RPC message or a batch of up to 32; requests answer `application/json`, notifications 202. There is no event stream or session, so GET and DELETE answer 405.
- `run_evaluation` with `wait: true` waits at most 10 minutes on the remote endpoint (one HTTP request); for longer evaluations call `get_report` until the status is done.

## Tools

| Tool | Reads or writes | What it does |
|---|---|---|
| `list_agents` | read | the org's agents with versions and capabilities (`GET /api/v1/agents`) |
| `list_benchmarks` | read | benchmarks and evaluation packs; with `agent`, which ones that agent can run |
| `estimate` | read | runs, cost range and minutes of an evaluation spec before it runs; unknown prices stay unknown |
| `run_evaluation` | write (scope `evaluate`) | starts an evaluation; with `wait: true` waits up to `wait_minutes` (default 30, at most 120) and returns the report card |
| `get_report` | read | the evaluation's status and report card: scores with 95% intervals per arm, benchmark and capability, reliability under faults, safety, the A/B verdict |
| `get_trace` | read | one run's trace as `json`, `jsonl` or `otlp`; cut at 200 KB with a note |
| `connect_agent` | write with `register: true` | detects the agent in a workspace folder (`path`: LangGraph, ADK, CrewAI, OpenAI Agents SDK, Pydantic AI, smolagents, DSPy, a plain function) or behind a URL or curl command (`target`), and says how it will run; with `register: true` creates it and writes `.heimdall/agent.yaml`. Never edits code. Literal secrets in `headers` are refused: pass `${ENV_VAR}` |
| `verify_agent` | read | checks an agent (server-side `agent.verify` when the server has it; for an agent served from the machine, whether `heimdall serve` is running; else one sample task) |
| `first_look` | write with `confirm: true` | plans a first look (5 tasks by 1 trial on the fastest benchmark the agent fits, with the estimate); with `confirm: true` starts it and returns the report card link |
| `explain_failure` | read | the failure patterns of an evaluation's benchmarks per arm (from triage), most common first, with how often each showed and the runs that show it; with `run_id`, the patterns of that run first and the run's outcome, grade and gates |

Errors come back as tool errors with Heimdall's own message, so a token whose scope is too narrow reads as "scope evaluate required" and the assistant can say so. Every answer keeps the server `mode`; only `live` is a real measurement.

The server speaks MCP revisions 2025-06-18, 2025-03-26 and 2024-11-05 (`initialize`, `ping`, `tools/list`, `tools/call`). Tests: `cmd/bifrost-bench/mcpserve_test.go` drives it over pipes against a fake Heimdall.

## Investigation tools

Nine read-only tools answer small, exact questions about runs, costs and the Inbox, each computed by a fixed function over the API. Every answer is cut at 64 KB with the line "cut: {shown} of {total} bytes; ask for a smaller page".

| Tool | Arguments (required in bold) | What it returns |
|---|---|---|
| `list_steps` | **run_id**, after_seq (0), limit (50, max 200), kind, agent_id, parent_seq | one page of the run's steps (`GET /runs/{id}/steps/page`), each summary cut at 300 characters; pass `next_after_seq` back as `after_seq` for the next page |
| `get_step` | **run_id**, **seq** | one step with the steps that witnessed it, the step it witnesses and its child steps (`GET /runs/{id}/step/{seq}`) |
| `list_agents_in_run` | **run_id** | the agents of a multi-agent run and how they were nested (`GET /runs/{id}/agents`), at most 200 |
| `get_conversation` | **run_id**, after_call (0), limit (5, max 20) | the run's model calls in order: model, status, the last message sent and the answer (text and tool calls), each cut at 4 KB; `kept: false` and "The gateway kept no text for this call." when the gateway stored no body |
| `get_retries` | **run_id** | per turn the count of retried attempts, the total retries, the total turns and the turns with any retry (at most 200 turns listed) |
| `compare_commands` | **run_a**, **run_b**, limit (100, max 300) | the shell commands of both runs, normalized (lowercase, spaces collapsed, numbers as `#`) and lined up by longest common subsequence; rows `{a_seq, b_seq, command, side}` with side `both`, `only_a` or `only_b`, and `first_difference` (row index) |
| `cost_per_task` | **exp_id**, group_by (`arm` or `condition`), top (20, max 100) | the experiment's cost explanation reduced to rows by cost, most expensive first, with the total when every row is priced. Grouping by task is not available yet: the cost explanation is kept per arm and condition |
| `todays_failures` | since_hours (24, max 168), limit (20, max 100) | Inbox items first or last seen in the window that are new, and accepted items that came back in the window: id, title, source, occurrences, flagged reasons, task |
| `get_inbox_item` | **id** | one Inbox item and its cleaned session preview, cut at 8 KB |
