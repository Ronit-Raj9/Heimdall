# Agents behind an OpenAI-compatible endpoint

Many agents are already served as an OpenAI-style API: a LangGraph or
Agents SDK app behind a `/v1/chat/completions` route, a LiteLLM or vLLM
front, an agent platform that speaks the Responses API. The `openai_agent`
adapter benchmarks such an agent as it is. You give the bench a URL; it sends
each task as a chat, runs the task's tools for the agent and grades the final
answer like any other agent's.

## Register one

In the web app: Agents > New > "OpenAI-compatible endpoint". It asks for:

- **Base URL**: `https://agent.example.com/v1`. The bench posts to
  `<base>/chat/completions`. A full URL ending in `/chat/completions` or
  `/responses` is used as it is (a `/responses` URL picks the Responses API).
- **Model name**: the `model` field sent. Empty uses the agent's model.
- **API key secret name**: the name of a secret in Settings > Secrets. Its
  value is sent as `Authorization: Bearer <value>`. Leave it empty for an
  endpoint with no key.

The YAML it writes:

```yaml
id: my-endpoint
adapter: openai_agent
image: bench/coding-core-base:1   # the bench's launcher runs here (needs python3)
model: claude-sonnet-4-5
openai:
  url: https://agent.example.com/v1
  api: chat                        # chat (default) or responses
  model: my-agent                  # optional, default model above
  api_key_secret: MY_AGENT_API_KEY # optional, an org secret name
  tools: auto                      # auto (default) or off
  max_turns: 20                    # optional, default budgets.turns, else 20
  system: "You are a support agent." # optional system message
  timeout_s: 600
budgets: {wall_s: 900, cpus: 1, mem_gb: 2}
gateway: {provider: openai, max_retries: 0}
```

Default capabilities: `tools`, `sessions`.

## What the bench sends

One run is a small loop in the bench's own launcher (`adapters/openaiagent.py`):

1. A request with the `system` message (when set), then the task
   instruction as a `user` message.
2. When the task has MCP tools (tool, support and MCP benchmarks), they are
   listed from the run's tool proxy and offered as OpenAI function tools
   (`tools: [{type: function, function: {name, description, parameters}}]`,
   or the flat Responses form). Two servers with a tool of the same name get
   `<server>__<tool>`.
3. Each tool call in the answer is run with MCP `tools/call` through the
   run's tool proxy, so it is recorded and policed like any agent's tool
   call. Its text goes back as a `tool` message (chat) or a
   `function_call_output` item (Responses). The bench resends the whole
   conversation each turn; it never relies on `previous_response_id`, and
   Responses requests are sent with `store: false`.
4. The loop ends on an answer with no tool call. That text is the answer the
   verifier grades. No answer after `max_turns` requests fails the run.

Conversation tasks (chat rows of your own data, a `chat` block in
`task.yaml`): the task's messages are sent as they are (its own `system`,
`user` and `assistant` turns after the agent's `system` message), with the
attachments as image or file parts of the last user message (a link as is,
a file stored with the task as a data URL). After each answer the next
scripted user turn is sent with the conversation so far. The last answer is
graded, or every turn as `role: text` lines when the task is graded on the
whole conversation.

Multi-session tasks (memory benchmarks): session N is sent with sessions
1 to N-1 as earlier `user` and `assistant` turns, the way a chat agent
remembers a conversation. The turns are kept in the run's memory volume
between sessions, and a new run starts with none.

## What is recorded

- The answer, graded by the task's verifier.
- Every tool call, at the tool proxy (the same evidence as for local agents).
- An agent trace in the agent event protocol: one `model_request` and
  `model_response` per request (with the endpoint's reported `usage` as
  tokens), one `tool_call` and `tool_result` per call, and the final answer.
- The endpoint's own model calls do not go through Bifrost, so gateway
  tokens, cost and steps show as unknown with the reason, never zero. The
  token counts in the trace are what the endpoint reported; they are not
  checked.

## Limits

- Answer tasks always run. State tasks run when the task has MCP tools and
  `tools` is `auto`. Diff and artifact tasks (coding benchmarks) do not run:
  the endpoint is not beside the workspace. The planner refuses them with
  the reason.
- Only MCP tools are offered. A task's plain HTTP services and external APIs
  are not turned into function tools.
- Streaming answers are not used; the bench asks for one JSON answer per
  request.
- The launcher reaches only the endpoint's host and port, through a per-run
  forwarder, the same as `remote_http`. Not on the Kubernetes driver.
- `agent check` and smoke runs refuse this adapter: the endpoint's model
  calls cannot be held to the bench's scripted model.

See also [`remote-execution.md`](remote-execution.md) for agents that take
the bench's own JSON request instead, and [`a2a-agents.md`](a2a-agents.md)
for A2A agents.
