# Agents that run in your own code (pull mode)

Some agents have no endpoint: a function in a Python or TypeScript process, a
LangGraph graph, an OpenAI Agents SDK script, or a service inside a private
network with outbound HTTPS only. Pull mode benchmarks them where they are.
Your process connects out to Heimdall and takes the evaluation's work; nothing
connects in to your network.

## Quick start

1. Create the agent. On Agents > New agent pick "Your own code (pull mode)",
   or write the config yourself:

   ```yaml
   id: support-bot
   adapter: pull
   image: bench/coding-core-base:1
   budgets: {wall_s: 900, cpus: 1, mem_gb: 2}
   pull:
     name: support-bot            # what your process serves; default the agent id
     # turn_timeout_s: 300        # how long one turn waits for your answer
     # tools: auto                # auto (default) | off
     # repo: tarball              # coding tasks: repo in, patch out
     # conversation: {driver: bench}   # chat tasks, docs/conversations.md
   ```

2. Serve it with an API token of your organization (scope evaluate or admin;
   on a local server, its write token):

   ```python
   import os
   from heimdall import serve

   def solve(turn):
       return my_agent(turn.instruction)          # a string, an Answer or a dict

   serve("support-bot", solve, url="https://heimdall.example.com",
         token=os.environ["HEIMDALL_TOKEN"], concurrency=4)
   ```

   ```ts
   import { serve } from "@heimdall/bench-client";

   await serve("support-bot", async (turn) => myAgent(turn.instruction), {
     url: "https://heimdall.example.com", token: process.env.HEIMDALL_TOKEN, concurrency: 4,
   });
   ```

3. Start an evaluation on the agent. The agent page shows whether a process is
   serving it, how many processes, when it last polled and the turns waiting.

Runs are refused while no process serves the agent: the run fails at its first
turn with "no process is serving pull agent ...: start heimdall.serve(...)".
Start `serve` first, then the evaluation.

Both SDKs use the standard library only (Python) or the global `fetch`
(Node 18 and later). Runnable examples are in `clients/examples/pull/`:
`plain_function.py`, `plain-function.ts`, `langgraph_agent.py`,
`openai_agents_sdk.py` and `coding_patch.py`.

## Turns

Each call of your function is one turn:

| `turn.kind` | When | Answer with |
|---|---|---|
| `task` | the task's first turn | the answer, tool calls, or a patch |
| `conversation` | a customer message, when the bench drives a chat | your reply to the customer |
| `tool_results` | the results of the tool calls you asked for are the last `messages` | the next step |

A turn carries `instruction`, `task_id`, `session_id`, `session_index`,
`output` (the task's output kind), `messages` (the chat so far, OpenAI roles),
`tools` (the task's tools: `name`, `description`, `input_schema`;
`turn.openai_tools()` gives them as OpenAI function tools), and for chats
`message` and `turn`, for coding tasks `repo`.

An answer has `text`, optional `tool_calls` (`[{id, name, arguments}]`),
`patch` (a unified diff), `usage` (`{input_tokens, output_tokens}`) and
`version`. In Python return `Answer(...)` (with `ToolCall(...)`), in
TypeScript a `PullAnswer` object. An exception fails the turn with its
message, and the run records it as the agent's failure.

## Tools

The task's tools never leave Heimdall. Your agent asks for a tool call in its
answer; the run's launcher calls the tool through the run's tool proxy (so the
tool ledger records it like any agent's call), and sends the results back as a
`tool_results` turn. This repeats until an answer has no tool calls, or
`max_tool_rounds` (default 20) is reached. State tasks need the task to have
MCP tools; `tools: off` offers none.

Your agent's model calls happen in your process, on your own keys, so token
and cost figures are those your answers report in `usage` (agent-reported, kept
apart from measured figures).

## Repo in, patch out

With `pull.repo: tarball` (or `remote.repo: tarball` for an HTTP endpoint) the
agent can run coding tasks. The task's first turn carries:

```json
{"repo": {"url": "https://heimdall.example.com/api/bench/pull/repos/repo_...?exp=...&sig=...",
          "format": "tar.gz", "sha256": "...", "bytes": 81234, "expires_at": "...", "files": 42}}
```

The link is signed and expires with the run. The tarball is the task's
workspace, the base tree only: hidden tests never leave the grader. Download
it (`turn.download_repo(dest)` unpacks it safely), make the change, and answer
with the unified diff (`git diff` output) as `patch`. The launcher checks the
patch (at most 8 MiB and 2000 files, no absolute paths, no `..`, nothing under
`.git`, no symbolic links or submodules), applies it to the workspace with `git apply`, and the bench exports
and grades the change with the clean-room grader exactly as an in-container
agent's. A patch that does not apply is recorded in the run's report and the
task is graded on the unchanged workspace.

Limits: a tarball is at most 48 MiB; an organization holds at most 512 MiB of
them at once. The report card marks these rows as `remote_patch`: the agent
worked in its own sandbox, so how the patch was made is not observed; only the
patch and its grade are the bench's.

## Security

- Your process authenticates with an org API token. It sees only its own
  organization's turns and can answer only turns it took.
- A run's launcher authenticates with a run token the bench signed at plan
  time (`hpr1.` prefix): it names the organization, the agent and the run, and
  expires with the run's wall budget plus 10 minutes. It is no account
  credential. It reaches the launcher only through its environment, redacted
  in `plan.json`, and is redacted from the launcher's output.
- The launcher's only route out of the run's network is Heimdall's pull
  endpoint, through the run's egress forwarder.
- A turn waits at most `turn_timeout_s`; 512 turns may wait per agent; a turn
  is at most 8 MiB and an answer at most 16 MiB.

## Routes

What `serve` calls, under `/api/v1` (also `/api/bench`):

| Route | Does |
|---|---|
| `POST /pull/next` | takes the next turn of an agent, waiting up to 25 s; 204 when none came. Every call marks the process as connected for 45 s |
| `POST /pull/turns/{id}/answer` | answers a turn: `{"answer": {...}}` or `{"error": "..."}` |
| `GET /pull/agents` | the organization's pull agents: connected, processes, last poll, queued and in-flight turns |

What a run's launcher calls with its run token: `POST /api/bench/pull/run/turns`,
`GET /api/bench/pull/run/turns/{id}` and `POST /api/bench/pull/run/repos`. The
signed repository link is `GET /api/bench/pull/repos/{id}?exp=&sig=`.

## Running the server

- A local server (`bifrost-bench serve`) needs nothing more, as long as its
  runs reach it at `auth.public_url`, or at `pull.url` in the bench config
  (`BENCH_PULL_URL`): an address on the host or the internet, never a
  compose-internal name. Without either, pull and repo-tarball runs are
  refused at plan time with that reason.
- On a hosted server the shared worker plans the runs and the server serves
  the routes, so both must hold the same `HEIMDALL_PULL_SIGNING_KEY`.
  `deploy/hosted/bootstrap.sh` generates it when empty and the compose file
  passes it to both, with `BENCH_PULL_URL` set to the public URL for the
  shared worker. Without a shared key the server refuses every run token
  ("the server and the worker that planned the run must share
  HEIMDALL_PULL_SIGNING_KEY").
- Pull agents run on the shared worker. A worker you bring yourself does not
  hold the server's signing key, so its pull runs are refused at their first
  turn.
- `pull.public_url` (`BENCH_PULL_PUBLIC_URL`) sets the origin of repository
  links when it differs from `auth.public_url`.
