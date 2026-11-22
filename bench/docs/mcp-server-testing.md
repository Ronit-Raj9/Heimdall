# Testing your MCP server

Most of Heimdall tests an agent. This mode tests an MCP server: the
organization's own server is the thing under test, and standard agents
(Claude Code, Codex, OpenCode) are the instruments. In the UI it is
**Test my MCP server** (`/mcp-test`, also linked from the first step of the
evaluate wizard).

There are three parts: a direct check of the server, a drafted benchmark that
reference agents run against the live server, and a per-tool report of those
runs. A fourth, the MCP-Atlas importer, brings public questions for servers
that offer the same tools as MCP-Atlas's.

## 1. Check the server

`POST /api/bench/mcp-servers/inspect` (member role), code in `mcptest/inspect.go`.

```json
{"url": "https://mcp.example.com/mcp", "secret": "SHOP_MCP_TOKEN", "auth_scheme": "Bearer",
 "call_read_only": true, "call_tools": [], "args": {"search_orders": {"query": "A-1001"}}, "repeats": 5}
```

The credential is the org secret named by `secret`, sent as
`Authorization: <auth_scheme> <value>` (or in `auth_header`). It never appears
in the answer. On a hosted server the URL must be public https and the client
dials through the guarded dialer (no private, loopback or metadata
addresses), and follows no redirect.

What it measures, each as a 0 to 100 score (the overall score is their mean):

| Score | How |
|---|---|
| Reliability | `initialize`, then `tools/list` and `ping` sent `repeats` times; the share that worked. A tool list that changes inside one session (admitscan `DiffListings`) costs 20 points. `ping` answered "method not found" is not a failure: it is optional in MCP. |
| Error handling | Deliberate bad requests: a tool that does not exist, an unknown method, a body that is not JSON, a tool call with its required arguments left out and one with wrong types (only on a tool that may be called), a session id the server never issued (only when it issues sessions), then a normal request. A clean refusal is a JSON-RPC error, an `isError` result or a 4xx; a 5xx, no answer, or a success is not. A refusal whose message is one word is noted. |
| Speed | p95 latency of every timed request: up to 300 ms is 100, 1 s 80, 3 s 50, 10 s 25, slower 10. Per method and per tool in `latency.by_method`. |
| Input schemas | Per tool (`mcptest/quality.go`): missing or invalid schema, type not object, parameters without description or type, ambiguous parameter names, required names that are not properties, deep nesting, many parameters. |
| Tool descriptions | Per tool: missing, short, only the name repeated, very long, no word saying what it returns or changes, vague or invalid tool names. Across tools: identical descriptions, names that differ only in case or separators, very many tools. |

Every issue carries a plain fix. The admission scan's MCP rules
(`admitscan.ScanMCPListing`: hidden instructions, shadowing other tools,
suspicious names) run over everything the server shows a model.

**Which tools are called.** None by default. With `call_read_only` the tools
the server marks `readOnlyHint` (and not `destructiveHint`) are called up to
three times with the sample arguments in `args`, else with placeholders built
from the schema (default, example, enum, or a plain value of the type).
Tools named in `call_tools` are called whatever their hints: name only tools
that are safe to call. A tool that is neither is never called, not even with
bad arguments. One check sends at most 80 requests.

## 2. Draft a benchmark

`POST /api/bench/mcp-servers/benchmarks` takes the same fields plus `id`
(the benchmark id), `server_id` (the name agents see the server under),
`tools` (default: all) and `max_tasks` (default 20, at most 60). It inspects
the server again, then writes `benchmarks/<id>/` into the org's project
(`mcptest/draft.go`), and answers 409 when the id exists.

- `suite.yaml` declares the server once as an external MCP server:

  ```yaml
  external:
    - id: shop
      base_url: https://mcp.example.com/mcp
      mcp: true
      mode: live
      auth_env: SHOP_MCP_TOKEN
      auth_scheme: Bearer
      max_calls: 60
      timeout_s: 30
  ```

- One task per tool, `tasks/tool-<name>/`. The instruction states the goal from
  the tool's own description and, when a sample call worked, the inputs it
  used; it never names the grading. Tasks are drafts:
  `provenance.instruction_reviewed: false` until a person reviews them.
- The verifier (`verifier/tests/verify.py`) reads only the tool proxy's ledger
  (`/grader/trajectory/tools.jsonl`), which the agent cannot write: `picked`
  (the agent called the tool), `valid_args` (one call's arguments fit the
  input schema), `succeeded` (one valid call got a result that is not an
  error). The reward is `succeeded`.
- Controls: the reference calls the tool with the arguments that worked during
  the check; the empty control does nothing; the mutant calls it with wrong
  types; the exploit claims success without a call and writes fake reward
  files. A tool that was not called during the check gets a reference with
  placeholder arguments marked NOT PROVEN, and is listed in `no_reference`:
  put arguments the server accepts into it before the readiness check.
- `mcp-server.json` keeps the listing the tasks were drafted from, for the
  report.

Then: run the readiness check on the benchmark page, review each task, and
evaluate it with the reference agents (the wizard opens with the benchmark
picked; add the other reference agents as setups).

### External MCP servers in the engine

`mcp: true` on an external API (`schemas.ExternalAPI.MCP`) turns the
[external API](external-apis.md) route into an MCP server route:

- agents get it as an MCP server: in `BENCH_MCP_SERVERS`, `{{mcp.<id>.url}}`
  and their native config, at `http://toolproxy:9200/x/<id>`; it is still in
  `BENCH_EXTERNAL_APIS` too;
- a call to that URL goes to the base URL itself (no trailing slash added);
- an empty allowlist allows POST and DELETE on the endpoint; GET (the optional
  server stream) is answered 405 by the proxy, and MCP clients then read
  answers from the POST replies;
- ledger rows name the tool of a `tools/call` (or the JSON-RPC method), and
  `is_error` also covers a JSON-RPC error and an `isError` result, so
  `distinct_tools` and `tool_error_rate` mean what they mean for local MCP
  servers;
- `mode: live` runs without the experiment opting in, because the live server
  is what is measured. Results depend on the server's state at run time and
  are not repeatable like a replayed cassette. Record and replay work as for
  any external API, but JSON-RPC ids and session ids differ between runs, so
  a replay rarely matches.

The credential stays in the proxy: the agent never sees it, and it is
redacted from the ledger, the logs and any cassette.

## 3. The server report

`GET /api/bench/mcp-servers/benchmarks/{id}/report[?experiment=<id>]`
(viewer role) reads the ledgers of every counted run of the benchmark and
answers, per tool and per agent: runs of the tool's own task, how many picked
the tool, calls, calls with valid arguments, successful calls, passed runs,
p50 and p95 latency, and advice when the numbers point at something (agents
rarely pick the tool for its own task; arguments often do not fit the schema;
valid calls fail on the server; the tool is slow). Tools agents called that
are not on the drafted list are listed apart. Ledger rows keep the first
`MaxRecorded` bytes of each call, so a call with very large arguments cannot
be checked and counts as not valid.

## 4. MCP-Atlas questions

`bifrost-bench import mcpatlas` reads the public subset of MCP-Atlas
(Scale AI, https://huggingface.co/datasets/ScaleAI/MCP-Atlas, CC-BY-4.0,
arXiv:2602.00933: 500 questions; Scale's held-out questions are not
included). Convert `MCP-Atlas.parquet` to JSON lines once (the importer
prints the command), or save the dataset viewer's rows answer.

```
bifrost-bench import mcpatlas -from MCP-Atlas.jsonl -suite mcp-atlas-github \
  -set servers=github=https://mcp.example.com/github,fetch=https://mcp.example.com/fetch \
  -set auth=github=GITHUB_MCP_TOKEN:Bearer
```

- A row's tools are named `<server>_<tool>`; each is mapped to a server given
  in `servers` by the longest prefix. A row that needs a server without a URL
  is skipped, naming the server, so giving only your own server imports
  exactly the questions it can answer.
- Each task declares its servers as external MCP servers (above) and is
  graded by the judge (`qa-v1`, rubric form): the answer must state at least
  75 percent of the row's claims, MCP-Atlas's coverage rule. Pin
  `judge.provider` and `judge.model` (written as `pin-me`) before locking.
  The judge prompt is the bench's, not MCP-Atlas's, so scores are not the
  leaderboard's.
- The reference answer states every claim; a mutant states one claim (for
  rows where one is below the coverage); the exploit addresses the grader.
  The reference trajectory is kept hidden in `verifier/`.
- The servers run live: a question whose data changed since MCP-Atlas was
  written can fail claims that were true then. `LICENSE-NOTICE.md` in the
  suite carries the attribution CC-BY-4.0 requires.
- Agents see every tool a server offers, not only the row's `ENABLED_TOOLS`
  (kept in the task's provenance); there are no distractor tools.
