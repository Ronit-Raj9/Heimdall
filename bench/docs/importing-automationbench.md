# Importing AutomationBench

`bifrost-bench import automationbench` turns the public tasks of
[AutomationBench](https://github.com/zapier/AutomationBench) (Zapier, MIT)
into suites of track `tool_use`: business workflows (sales, marketing,
operations, support, finance, HR) over simulated SaaS apps with real state,
graded by upstream's assertions on the final state of those apps.

## What upstream is

Checked at commit `4a8e106` (v1.0.6, 4 Aug 2026).

- Licence: the LICENSE file is MIT (GitHub shows NOASSERTION because the file
  adds a "scope of license" note: the third-party API shapes the simulated
  apps copy are not claimed as Zapier's work). Code, tasks and simulation
  logic are MIT.
- 800 tasks, defined in Python (`automationbench/domains/<domain>/tasks.py`):
  600 scored (sales, marketing, operations, support, finance, hr, 100 each)
  and 200 `simple` single- and two-step tasks that upstream leaves out of the
  score. The official leaderboard uses a private held-out set, so scores here
  are not comparable with it.
- Each task: a system prompt and a user request (no user interaction), an
  initial state of the simulated world (47 apps: Gmail, Google Sheets, Google
  Drive, Slack, Salesforce, HubSpot, Zendesk, Jira, Calendar, Zoom, Asana,
  Trello, Airtable, QuickBooks, Xero and more, with deterministic "production
  noise" rows), the Zapier tools it allows, and a list of assertions on the
  final state (337 assertion types).
- Grading: `partial_credit` is the fraction of scored assertions that hold;
  an assertion that already holds before the agent acts is left out (and
  counts as a failure if the agent breaks it). `task_completed_correctly`
  (every scored assertion holds) is upstream's pass rate.
- Toolsets: `api` (default, used for the leaderboard: `api_search`,
  `api_fetch` over REST-style URLs of every app, `base64_encode`), `zapier`
  (search and execute meta tools) and `limited_zapier` (the task's own
  Zapier tools).
- No reference solutions.

## Getting the data

The tasks are Python, so the service image exports them:

```
docker build -t bench/automationbench:1 bench/importers/automationbench/image
docker run --rm --network none -v <ds>:/out bench/automationbench:1 \
  python /opt/abench/abench.py export --out /out/tasks.jsonl
```

Each import also writes the same build folder to
`<suite>/services/automationbench/`. The
export holds every task as one JSON line: domain, example id, task name,
prompt, initial state, assertions, Zapier tools, the services the task's
world is subscribed to (upstream's `compute_allowed_services`) and upstream's
task contract hash.

## Importing

```
bifrost-bench import automationbench -from <ds> -suite automationbench -set domains=public \
  -set references=bench/importers/automationbench/references
bifrost-bench import automationbench -from <ds> -suite automationbench-simple -set domains=simple \
  -set references=bench/importers/automationbench/references
bifrost-bench suite lock automationbench
bifrost-bench suite lock automationbench-simple
```

Settings: `domains` (comma list of sales, marketing, operations, support,
finance, hr, simple, or `public` for the six scored ones; default all),
`toolset` (`api`, the default, or `limited_zapier`), `image` (default
`bench/automationbench:1`), `revision`, `references` (folder of
`<task id>.py` reference bodies), `personas` (folder of `<task id>.json`
simulated colleagues, below). Task ids are `ab-<domain>-<task name without
the domain>` with `_` as `-`, e.g. `ab-sales-multi-hop-lookup`.

## What a task looks like

- One service `apps` (role `mcp`, port 8080, image `bench/automationbench:1`):
  `abench.py serve` builds upstream's `WorldState` from the task's initial
  state (`/task/scenario.json`, which holds no assertions), sets the
  subscribed services, and serves the toolset's tools over MCP streamable
  HTTP (JSON, no event stream). Tool schemas come from the upstream function
  signatures and docstrings, as upstream's verifiers library builds them;
  an empty object argument means "no value", as upstream. After every call
  the world is written to `/state/world.json` and the call log to
  `/state/calls.json`; both are exported after the agent exits.
- Verifier `abench.py verify`, in the same image with no network: upstream's
  `partial_credit` on the exported world against the hidden assertions and
  initial state (`verifier/tests/grading.json`). The reward is
  `partial_credit`; the suite's pass threshold is 1, so a pass needs every
  scored assertion (upstream `task_completed_correctly`). Dimensions:
  `partial_credit`, `task_completed_correctly`. `details.json` lists every
  assertion as pass, fail or excluded.
- Run clock: every task has `clock:` (docs/scenarios.md). A task whose
  initial state sets `meta.current_time` gets that time; the others (upstream
  then uses the wall clock) get `2026-08-04T09:00:00Z`, the upstream release
  day, so "tomorrow" means the same day on every run. The image preloads
  libfaketime, so upstream's own `datetime.now()` calls see the clock too, and
  `BENCH_NOW` fills `meta.current_time` when the task has none.
- Instruction: a line naming the server and the task's apps, upstream's
  system prompt, then the request.
- Controls: `empty.sh` (no calls), `exploit.sh` (claims success, no calls),
  and `reference.sh`: upstream ships none, so a task without a written
  reference refuses and is not admitted. 35 references are in
  `bench/importers/automationbench/references/` (14 simple, 21 scored across
  all six domains), each checked to score 1.0; they call the same MCP tools
  an agent gets (through `refclient.py`).

## Simulated colleagues (personas)

`-set personas=<dir>` adds people who reply to a task (none of upstream's
tasks has them; this is for tasks you write or extend). `<task id>.json`:

```json
{"mode": "scripted",
 "personas": [{"id": "dana", "name": "Dana Lee", "role": "Finance lead",
               "email": "dana@acme.example.com", "slack_user_id": "U_DANA",
               "facts": ["Q3 budget is $40,000"],
               "rules": [{"if_contains": ["budget"], "reply": "The Q3 budget is $40,000."}],
               "default_reply": "Thanks, noted.", "max_replies": 5}]}
```

When the agent emails a persona (to or cc) or writes to one in Slack (a DM
channel they are in, a `<@ID>` mention, or their name in a channel they are a
member of), the reply appears in the same email thread or Slack thread, read
with the normal tools. `scripted` answers from the rules (first rule whose
words all appear), else `default_reply`; `llm` lets a pinned model play each
persona from its role, facts and behaviour through the gateway on the
service's own virtual key (the service gets `gateway: {}`), never the
agent's. Every reply is a row in `calls.json` (`personas.persona_messages`);
a failed model call adds no reply and counts in `persona_errors`. The
instruction lists the colleagues and how to reach them.

## Not comparable with the leaderboard

The public set differs from the private leaderboard set, the agent is yours,
and two upstream paths are offline here: with `OPENAI_API_KEY` upstream asks
a model for Salesforce SOSL search and some SOQL filters, and for the ChatGPT
app's completions. In the image those fail as upstream does without a key
(the ChatGPT app returns upstream's simulated reply). The `zapier` meta
toolset is not offered.
