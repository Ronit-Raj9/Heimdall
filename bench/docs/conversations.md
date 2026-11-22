# Conversations the bench drives

A support or sales agent in production receives a customer message and
replies. It never calls a "speak to the user" tool. Heimdall's support
benchmarks (tau2-style tasks, the acme-helpdesk example) hold the simulated
customer as a task service, and by default the agent talks to it through the
`respond_to_user` tool. That default suits agents that run inside the task
container. For a chat agent that lives somewhere else (an HTTP endpoint, an
OpenAI-compatible endpoint, an A2A agent or a pull agent), the bench can drive
the conversation instead.

## Turning it on

Add a `conversation` block to the agent's remote, OpenAI-compatible, A2A or
pull block:

```yaml
adapter: remote_http
remote:
  url: https://support.example.com/chat
  conversation:
    driver: bench        # agent (default) | bench
    history: client      # client (default) | server; see below
    max_turns: 12        # agent replies; default the simulator's max_turns, else 12
    greeting: "Hi! How can I help you today?"   # the agent's first line, optional
```

The same block goes under `openai:`, `a2a:` or `pull:`. It applies only to
tasks that have a simulated customer (a service with role user, or a tau2
domain server that holds its own user). Every other task runs the agent once,
as without the block.

## What happens in a run

1. The bench's launcher sends the greeting to the simulated customer, as the
   agent's first line (tau2-bench's default opening, so imported tau2 tasks
   start the way they do there).
2. The customer's reply is sent to the agent as one chat turn.
3. The agent's reply goes back to the customer.
4. Steps 2 and 3 repeat until the customer's reply carries `###STOP###` or the
   agent has replied `max_turns` times.

The agent is never offered `respond_to_user`. Its other tool calls still go
through the run's tool proxy, so the tool ledger and the state checks work as
for any agent.

The transcript is written by the simulated customer, in its own container,
from the messages the bench passed in. It never passes through the agent, so
it is trusted evidence rather than agent-reported. Each message the bench
sends carries `_meta.heimdall.driver: bench`, which the simulator records in
the transcript (`"driver": "bench"`). The launcher also prints one
`conversation_turn` event per turn, and the run's report names how the chat
ended (`user_stop` or `max_turns`).

## History: who keeps the conversation

A stateful agent keeps the conversation itself; a stateless one needs the
whole chat with every turn. The default never skews either kind:

| Agent kind | What each turn carries | `history` |
|---|---|---|
| HTTP endpoint (`remote`) | the new `message`, the conversation's `session_id`, the `turn` number, and the whole chat as `messages` unless `history: server` | `client` (default) or `server` |
| Pull agent (`pull`) | `turn.message`, `turn.session_id`, `turn.turn` and the whole chat in `turn.messages` | either; both are always sent |
| OpenAI-compatible (`openai`) | the whole chat as `messages`, since the protocol keeps no state | `client` only; `server` is refused |
| A2A (`a2a`) | only the new message, on the same A2A context | `server` only; `client` is refused |

So an HTTP endpoint that keeps its own state reads `conversation.message` and
`session_id` and ignores `messages`; one that keeps none reads `messages`. Set
`history: server` when the endpoint should not receive the chat it already
holds (smaller requests, or an agent that would read both).

## The HTTP request

With the bench driving, each customer message is one `POST` to the
endpoint, with the usual body (docs/remote-execution.md) plus:

```json
{"output": "conversation",
 "conversation": {"turn": 2, "message": "It is order B-3001.", "history": "client",
                  "messages": [{"role": "assistant", "content": "Hi! How can I help you today?"},
                               {"role": "user", "content": "Hi, my grinder arrived cracked."},
                               {"role": "assistant", "content": "Sorry about that. Which order is it?"},
                               {"role": "user", "content": "It is order B-3001."}]}}
```

The reply is read like any answer: `{"answer": "..."}` or a text body. Usage a
deployment reports is summed over the turns.

## Grading

tau2 checks and the helpdesk checks read the transcript and the tool state as
they always do. Since the bench drove the chat, the report card lists the
conversation among what is trusted for the run.
