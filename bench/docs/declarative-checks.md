# Declarative checks (checks.yaml)

A task can say what a correct agent does as data instead of a Python
verifier: a `checks.yaml` in its hidden verifier folder (the folder
`verifier.tests` names, `verifier/` when unset). One shipped grader reads it,
so a support, tool-use or MCP task needs no code to grade.

The benchmark selects it with `grader: checks` in `suite.yaml`. A task
without a `checks.yaml` in such a benchmark is still graded by the
benchmark's verifier command, so a benchmark can move to checks one task at
a time.

## What a check reads

After the run the grader reads three things the bench recorded, never files
the agent wrote:

- the tool proxy's ledger (`tools.jsonl`): every tool call the agent made,
  with its arguments and whether it returned a tool error;
- the services' exported state (`state/<service>/<export id>`), JSON or
  SQLite;
- the conversation: the simulated customer's transcript when the task has
  one, else the answer.

## The file

```yaml
version: 1
pass_threshold: 1          # weighted share of checks that passes the task (default 1: all)
checks:
  - id: refunds-order
    description: the agent refunds the duplicate charge
    required: true          # the task fails when this check fails, whatever the score
    tool_called:
      tool: issue_refund
      server: helpdesk      # optional; empty matches any service
      args: {order_id: B-3001}          # subset match (default); match: exact for the whole object
      args_regex: {reason: "^(duplicate|double)"}
      args_path: [{path: "$.amount_pence", lte: 4500}]
      succeeded: true       # only calls that returned no tool error
      times: 1              # or min_times / max_times
  - id: no-credit
    tool_not_called: {tool: apply_credit}
  - id: verify-first
    call_order: [verify_identity, issue_refund]   # a bare name is a tool
  - id: order-refunded
    weight: 2
    state:
      file: helpdesk/db.json
      path: "$.orders[?(@.id=='B-3001')].status"  # or the slash form orders/id=B-3001/status
      equals: refunded
  - id: one-ticket
    state: {file: crm/db.sqlite, sql: "SELECT count(*) FROM tickets WHERE customer_id = 'C-1'", equals: 1}
  - id: tells-customer
    reply_rubric: Confirms the refund and says it arrives within 5 business days.
```

Value tests on a path or a query: `equals`, `not_equals`, `in`, `contains`,
`not_contains`, `matches` (regular expression), `gte`, `lte`, `length`,
`max_length`, `exists`; `all: true` applies them to every match of a path
with a wildcard or a filter.

Every check has a weight (default 1; 0 reports it without counting it). The
reward is the weighted share of checks passed. Each check is also its own
reward dimension (`check.<id>`), so a report card shows which one failed.

`reply_rubric` is judged by the benchmark's pinned judge model
(`grading: judged` with a `judge:` provider and model) behind the judge
guard: the conversation and the tool calls are fenced as data, and a reply
that tries to instruct the judge fails. Without a pinned judge, `suite lint`
refuses a rubric.

`suite lint` and the readiness checks read the file: unknown keys, an empty
check, a state file no service exports, or a rubric without a judge are
errors, and admission still requires the reference to pass every check and
each mutant to fail.

## Writing checks without the file

- **Support conversations** (Build tab, "Support conversations", or `POST
  /api/bench/suites/{id}/scenario-rows`): CSV or JSON Lines rows with
  `opening_message`, `persona_goal`, `facts`, `unknown`, `account_seed`,
  `expected_actions`, `forbidden_actions`, `ordered`, `reply_rubric` become
  tasks with a simulated customer, the account merged into the starting
  data, a `checks.yaml`, a reference that makes the expected calls and
  mutants that skip the key call or also make a forbidden one.
- **Production failures** ("What should have happened?" on an item, or `guide` on
  `POST /api/bench/inbox/{id}/accept`): the recorded calls marked right or
  wrong, plus any missing call and a reply rubric, become the task's
  `checks.yaml`, reference and mutants. See docs/failure-inbox.md.
