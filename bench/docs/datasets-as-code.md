# Benchmarks as code: the dataset API

Teams that keep their evaluation data in code (a CSV in the repository, a LangSmith dataset, last week's production traces) manage question-and-answer benchmarks through API v1 (`docs/api-v1.md`) or the Python and TypeScript clients (`clients/`). Every change to a benchmark's rows is a new, numbered version with a hash, so an evaluation can say exactly which rows it ran on.

## Routes

| v1 route | What it does |
|---|---|
| `POST /api/v1/benchmarks` | Create a benchmark from a template (`{"id": "faq", "template": "qa", "blank": true}`); 409 when it exists |
| `PUT /api/v1/benchmarks/{id}/grader` | Choose `answer_match` (no model) or `judge` with its pinned judge model |
| `GET /api/v1/benchmarks/{id}/tasks` | List tasks with metadata, attachments and row hash; expected answers are never listed |
| `POST /api/v1/benchmarks/{id}/tasks` | Add or replace tasks from rows; `dry_run` previews; `mode` is `upsert` (default) or `add` |
| `DELETE /api/v1/benchmarks/{id}/tasks/{task}` | Remove a task made from rows (a new version) |
| `GET /api/v1/benchmarks/{id}/versions`, `GET /api/v1/benchmarks/{id}/versions/{n}` | The version log, newest first; one version with the row hash of every task |
| `GET, PUT /api/v1/benchmarks/{id}/data-policy` | Personal data mode and who may see the benchmark's task content |
| `POST /api/v1/imports/platforms` | Import a dataset or recent traces from LangSmith, Langfuse, Braintrust or Datadog |

Writes need a token with scope `admin` (or the local write token). Hand-written tasks (folders under `tasks/`) are listed with `from_rows: false` and are never changed by these routes.

## Rows

```json
{
  "id": "refund-window",
  "input": "How long do I have to ask for a refund?",
  "expected": ["30 days"],
  "metadata": {"tier": "pro", "language": "en"},
  "attachments": [{"name": "policy.md", "text": "Refunds within 30 days."}]
}
```

`input` is the question or customer message; `messages` (a list of `{role, content}`) is a chat input instead, and the agent answers the last user message. `expected` holds the accepted answers for `answer_match` or the reference for a judge; `rubric` says what the judge checks; `held_out` keeps the task in the private split. Metadata values are strings, numbers or booleans. Attachments are text or `content_base64` (binary) files given to the agent with the task.

A row whose `id` names a task made from rows replaces it; an unchanged row is reported in `unchanged` and makes no version. A row without an id gets one from its title or input. Rows that cannot become tasks come back in `problems` with their 1-based index; the others are still written.

## Personal data and access

Every write runs the benchmark's personal data policy first. The scan looks for emails, phone numbers, card numbers, national ids, bank accounts and names. Modes:

| Mode | Effect |
|---|---|
| `warn` (default) | Report what was found, keep the text |
| `mask` | Keep the shape, hide most characters (`j***@example.com`, card ending 4242) |
| `redact` | Replace with a stable placeholder: the same value gives the same placeholder in every row |
| `block` | Refuse rows that hold personal data |
| `off` | No scan |

`kinds` limits the scan to some kinds. The answer's `pii` field says what was found and what was done, per row.

Access: `org` (default) lets every member see the benchmark's task content; `restricted` limits it to org admins, owners and the user ids listed. Only an org admin or owner changes access. Evaluations still run on a restricted benchmark; people outside the list see scores, not the task text.

## Imports from other platforms

Add the platform's keys under Settings, Secrets first. Default secret names:

| Platform | Secrets | `dataset` | `project` |
|---|---|---|---|
| LangSmith | `LANGSMITH_API_KEY` | dataset name or id | project for traces |
| Langfuse | `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY` | dataset name | (traces of the whole project) |
| Braintrust | `BRAINTRUST_API_KEY` | dataset id | project id for logs |
| Datadog LLM Observability | `DD_API_KEY`, `DD_APP_KEY` | (none) | ml app |

`secrets` maps a role (`api_key`, `public_key`, `secret_key`, `app_key`) to another secret name when yours differ. `target: "benchmark"` turns records into tasks of `benchmark` (the same rules and personal data policy as `POST /tasks`); `target: "inbox"` puts traces in the failure inbox for triage. `mapping` picks the input, expected and rubric fields by dotted path when the guess is wrong. `GET /api/bench/imports/platforms` lists the platforms and which secrets the organization has. Always start with `dry_run: true`: it reads the records and shows a preview without writing anything.

## Python

```python
from heimdall import V1Client
hd = V1Client("https://heimdall.example.com", token=os.environ["HEIMDALL_TOKEN"])
hd.create_benchmark("faq")
hd.set_grader("faq", "answer_match")
hd.set_data_policy("faq", pii_mode="redact")
res = hd.upsert_tasks("faq", rows, source="git:" + commit)
print(res["version"])
```

## TypeScript

```ts
import { V1Client } from "@heimdall/bench-client";
const hd = new V1Client("https://heimdall.example.com", { token: process.env.HEIMDALL_TOKEN });
await hd.upsertTasks("faq", rows, { source: `git:${commit}` });
await hd.importFromPlatform({ platform: "langfuse", kind: "dataset", dataset: "support-faq", target: "benchmark", benchmark: "faq", dry_run: true });
```
