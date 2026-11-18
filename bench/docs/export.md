# Export: tasks, results and traces in formats other tools read

Heimdall's tasks, results and traces leave in formats the rest of the ecosystem reads: Harbor task folders, Every Eval Ever result records, a flat run table, and OpenTelemetry traces shaped for each observability backend. Everything is read from data the bench already locked or sealed; nothing calls a model.

The code is `bench/export/` and the `export` subcommands in `cmd/bifrost-bench/export.go`. The trace layout itself is in [trace-export.md](trace-export.md).

| Command | API | What |
|---|---|---|
| `export harbor -suite <s> [-tasks a,b] -out <dir> -include-hidden` | `GET /suites/{id}/export?format=harbor` (tar.gz, verifiers withheld) | each task as a Harbor task folder |
| `export eee -exp <id> [-out file\|dir/]` | `GET /experiments/{id}/export?format=eee` (JSON lines) | one Every Eval Ever record per arm |
| `export jsonl\|csv -exp <id> [-out file]` | `GET /experiments/{id}/export?format=jsonl\|csv` | one row per sealed run |
| `export otlp -exp <id> -to <backend>` | per run: `GET /runs/{id}/otlp` | traces with the backend's endpoint, headers, encoding and attributes |
| `export inspect` | none | not written; says why (below) |

The UI has an Export menu on the verdict page (EEE, JSONL, CSV) and on the suites page (Harbor tar). The menu links are plain downloads from the bench API; trace export to a backend stays on the CLI because it needs that backend's credentials.

## What was checked, and when

All formats were read from their own documents on 18 Nov 2026 (the web search quota was exhausted, so only known URLs were fetched):

| Format | Source read | Version or commit |
|---|---|---|
| Harbor task format | docs.harborframework.com `tasks/overview`, `tasks/configuration`, `tasks/verifier`, `tasks/solution`, `datasets/create-a-dataset` (the `.md` pages) | task.toml `schema_version` default `"1.4"`; harbor-framework/harbor `main` at `e6c387d2` (18 Nov 2026) |
| Harbor tasks in practice | Terminal-Bench 2.0 and SkillsBench folders already under `bench/datasets/` (read only) | as imported on 15 Nov 2026 |
| Every Eval Ever | `every_eval_ever/schemas/eval.schema.json`, `validator/validation_core.py`, `.agents/skills/eee-dataset-conversion/reference/fields.md`, README | evaleval/every_eval_ever commit `0791adc3c2488cc8c91d109b3c541da492238e49` (main, 11 Oct 2026; repository pushed 16 Nov 2026), schema `0.3.0`, MIT |
| Inspect logs | inspect.aisi.org.uk `eval-logs.html` | page as served 18 Nov 2026 |
| Langfuse | langfuse.com `integrations/native/opentelemetry` | page as served |
| LangSmith | docs.langchain.com `langsmith/trace-with-opentelemetry` | page as served |
| Braintrust | docs.braintrust.dev `integrations/sdk-integrations/opentelemetry/send-traces-and-logs` and `.../attributes` | pages as served |
| Datadog | docs.datadoghq.com `llm_observability/instrumentation/otel_instrumentation` and `opentelemetry/setup/otlp_ingest/traces` | pages as served |
| Maxim | getmaxim.ai `docs/tracing/opentelemetry/ingesting-via-otlp` | page as served |
| Phoenix | arize.com `docs/phoenix/tracing/concepts-tracing/otel-openinference/{exporter,resource,span-kinds,semantic-conventions}` | pages as served |
| Jaeger | jaegertracing.io `docs/latest/architecture/apis` (2.23); container `jaegertracing/jaeger:2.11.0` | run locally |

No real Langfuse, LangSmith, Braintrust, Datadog, Maxim or Phoenix backend was called. Every preset is tested against a local fake collector. Jaeger was called for real: the `hd-w10-jaeger` container (jaeger 2.11.0) took a run as OTLP/JSON and another as OTLP protobuf, and its query API returned both traces with all 12 spans and the attribute types intact (removed afterwards).

## Harbor tasks

`export harbor` writes one folder per task, the layout of the Harbor docs:

```
<out>/<task id>/
  task.toml  instruction.md  README.md
  environment/Dockerfile  environment/repo/...     the image and the task repository
  solution/solve.sh  solution/patch.diff | reference.sh | answer.txt
  tests/test.sh  tests/heimdall/{check.py, hidden/, restore/, required.txt, answer.json}
```

A Harbor task carries its verifier, so the folder holds the hidden tests, the expected answer and the reference solution. The command refuses without `-include-hidden` and warns when it is given. Held-out tasks are never exported. `-admitted-only` keeps only tasks the suite lock marks admitted. The API serves a tar of the admitted public tasks with every verifier and solution withheld: its `tests/test.sh` exits 3 without a reward, and `task.toml` says `heimdall_verifier = "withheld"`.

What maps:

| Heimdall | Harbor |
|---|---|
| suite base image (`base_image.dockerfile` and its folder) or the task's own image (`environment.dockerfile`/`context`, or `image` as `docker_image`) | `environment/Dockerfile` and build context |
| task repo folder | `environment/repo/`, copied to the suite workdir and committed as a git baseline, owned by the image's final user |
| `instruction.md` | `instruction.md` |
| reference control: a patch, a script or an answer | `solution/solve.sh` applying `patch.diff`, running `reference.sh`, or writing `answer.txt` |
| suite verifier command, restore list, result format (junit, reward, exit code), required tests | `tests/test.sh`: resets the restore paths from the pristine repo, lays out the hidden tests under `/tmp/hd-grader` (every `/grader` in the command and in the hidden test files is rewritten to it), runs the command with the suite timeout and writes `/logs/verifier/reward.txt` through `check.py` |
| pass rule of `hidden_tests` (exit 0, more than zero tests, no failures, errors or skips, every required test ran and passed) | `check.py junit`, the same rule; a missing or empty report is a 0, never an error |
| `answer_match` with `exact`, `contains_all`, `regex`, `numeric` (one distinct number, digits with thousands commas or the words zero to twenty, within the tolerance) and `choice` (the option label or the whole answer), with the four normalizers | `check.py answer`, ported rule for rule; a test runs 100 answers through both and they agree |
| `timeouts.agent_s`, `verifier.timeout_s` | `[agent] timeout_sec`, `[verifier] timeout_sec` (plus 60 s) |
| id, version, kind, difficulty, tags, capabilities, lock hashes, provenance | `[task] name = "heimdall/<suite>-<task>"`, `description`, `keywords`, root `source`, `[metadata]` |

Shapes written: diff (graded in place with the restore paths reset), snapshot (graded in the agent's live container), artifact (the declared files are copied to where the verifier reads them) and answer (from the answer file). Skipped with the reason: judged suites (the grade needs a judge model and key), anything with services (databases, tool servers, simulated users), multi-session tasks, tasks that call external APIs through the recording tool proxy, impossible tasks, state-graded tasks, `json` and abstain answers, symlinks, and tasks that were themselves imported from Harbor (the upstream folder is the Harbor task; wrapping the importer's wrapper again would nest two verifiers). A diff task with its own image is graded in place without resetting the restore paths, because its pristine workspace lives in the image, not in the export; its README says so.

What is lost, and every task's README.md says so:

- **The verifier runs in the agent's container** (Harbor's shared verifier, as root). The bench grades a diff applied to a fresh copy, with no network, the workspace read-only and the hidden files arriving only then. Harbor's separate verifier would be closer, but the bench's own importer skips separate-verifier tasks, so the export uses shared verification to keep the round trip.
- **No network rule.** The bench gives the agent no network except model calls through its gateway; Harbor's default is public.
- **No probes, fault conditions, integrity gates, cost or token accounting**: these belong to the bench, not the task.
- **The project notes fixture is not installed**: the bench writes it under each agent's own notes file name.
- **An answer task reads only the answer file**: the bench falls back to the agent's final message, which Harbor does not give the verifier.
- **python3 must be in the image** to read the verifier result; `test.sh` exits 3 (no reward) without it.

Round trip (tested): every coding-core task exports, imports back with `import harbor` with none skipped, and the imported suite loads (`TestHarborRoundTrip`). In docker at $0 on 18 Nov 2026: coding-core `t01` was exported, imported into a scratch project (`import harbor`), locked and admitted by `suite admit`: reference 3 of 3 passed through the exported `test.sh` (run by the importer's snapshot verifier), the empty control, the exploit (a planted reward) and the noop and refuse agents failed, the verifier had no network. The first attempt failed at image build (`git init` as root in a folder owned by the agent user: "dubious ownership"); the Dockerfile now passes `safe.directory` to git. The same round trip admitted an answer task (`hotel-cap` of the `qa` template, numeric match) and an artifact task (`sales-report` of the `artifact` template, reward result), every control as expected.

Run the way Harbor runs a task, by hand (`docker build` of `environment/`, the solver as the image user in the workdir, `tests/` copied to `/tests`, `tests/test.sh` as root, no network): coding-core `t01` scored 1 with `solution/solve.sh`, 0 after coding-core's own exploit control (a root `conftest.py` that turns failures into passes; the restore step removes it), and 0 with nothing done. Harbor itself was not run: its CLI is a Python package that would need network to install.

## Every Eval Ever records

`export eee` writes one aggregate record (an `EvaluationLog`) per arm: one agent config, one model, one suite. Each condition the experiment ran is one group of `evaluation_results`, computed by the same code as the leaderboard and the matrix (`leaderboard.Matrix`):

| Result (`metric_id`) | Score | Uncertainty and details |
|---|---|---|
| `heimdall.verified_success` | mean over tasks of the per-task verified success rate, 0 to 1 | 95% t interval over tasks (descriptive), `num_samples` = graded runs; passes, unknown runs, tasks, rank, tier |
| `heimdall.pass_hat_k` | mean over tasks of pass^k, `metric_parameters.k` | `num_samples` |
| `heimdall.cost_per_verified_success_usd` | total observed cost over verified successes | lower is better, 0 to Infinity |
| `heimdall.cost_usd_median`, `heimdall.tokens_median`, `heimdall.wall_time_s_median`, `heimdall.steps_median` | medians per run | `num_samples` |
| `heimdall.false_completion` | share of runs claiming success the verifier did not confirm | `num_samples` |

A result no run observed is left out, never written as zero. Metric ids are namespaced, as the EEE field guide asks for benchmark-specific constructs.

The rest of the record: `model_info.id` is `<provider>/<model>` (the schema's form for API models; not resolved against the EEE registry, which would be a network call), `inference_platform` the gateway provider, `deployment_type: externally_managed`, `model_availability: unknown`. `eval_library` is `heimdall` and the bench version. `generation_config` carries the sandbox (docker and the agent image), the time and turn budgets in `eval_limits`, the adapter and agent config; the rest goes to `additional_details`. `source_metadata.source_type` is `evaluation_run` (the bench holds the per-run outputs); `source_organization_name` comes from `-org` (default `unspecified`) and `evaluator_relationship` from `-relationship` (default `third_party`). Heimdall provenance lives in the schema's extension area, `source_metadata.additional_details`: experiment and arm, manifest hash, suite lock hash, fingerprint hash, the sorted seal hashes of the arm's runs and their sha256, the grade source and the synthetic flag. `evaluation_id` is `heimdall.<suite>/<experiment>/<arm>`, stable across exports; `retrieved_timestamp` is the export time in Unix seconds.

Synthetic results (scripted agent, scripted model or a smoke experiment, by the leaderboard's rule) are refused unless `-allow-synthetic` (API: `allow_synthetic=true`); then each record says `heimdall_synthetic: "true"` with a note.

Validation: every record is checked before it is written against the vendored schema (`export/schemas/eee/eval.schema.json`, unchanged, with its MIT licence and a SOURCE note) by `santhosh-tekuri/jsonschema` as draft-07, plus the upstream validator's score check (a continuous metric has bounds and its score lies in them). The golden record was also checked with Python `jsonschema` 4.26 (`Draft7Validator`): 0 errors. The upstream pydantic validator (`every_eval_ever validate`) was not run: fetching its models failed with connection resets, and it checks the datastore path layout (`data/<collection>/<developer>/<model>/<uuid>.json`), which a local export does not use. Not written: the instance-level `_samples.jsonl` companion.

Output: one record per line on stdout, one file for a one-arm experiment, or `<exp>-<arm>.json` per arm in a folder (`-out dir/`).

## Run table

`export jsonl` and `export csv` write one row per sealed counted run (the runs analysis reads, each checked against its seal): experiment, run, slot, pair, suite, task and version, arm and label, agent config, model, provider, condition, trial, attempt, outcome, verified success (1, 0, or empty when the outcome does not score), the grade's pass, grade source, cost, synthetic, start and seal time, manifest and seal hash, evidence problems, and every metric the run recorded. CSV adds one `metric.<id>` column per metric id; a metric the run did not observe is empty (JSONL: `null`). Free-text cells that start like a spreadsheet formula get a leading `'`. The JSONL row's keys are pinned in `handlers/testdata/contract/ExportRunRow.keys`.

## Trace presets

`export otlp -to <backend>` builds each run's trace as [trace-export.md](trace-export.md) describes, adds the backend's attributes and posts it. Credentials come only from environment variables (never a flag); a missing one stops the export with the variable's name. `-endpoint` overrides the URL; `-header-env` adds headers; `-out` also writes the mapped OTLP/JSON.

| Preset | Traces URL | Headers (from) | Encoding | Attributes added |
|---|---|---|---|---|
| `langfuse` | `https://cloud.langfuse.com/api/public/otel/v1/traces` (EU) | `Authorization: Basic base64(pk:sk)` (`LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY`), `x-langfuse-ingestion-version: 4` | JSON | `langfuse.observation.type` (generation, tool, agent, evaluator, guardrail, span), `langfuse.trace.name`, `langfuse.session.id`, `langfuse.trace.metadata.*` |
| `langsmith` | `https://api.smith.langchain.com/otel/v1/traces` | `x-api-key` (`LANGSMITH_API_KEY`), optional `Langsmith-Project` (`LANGSMITH_PROJECT`) | protobuf | `langsmith.span.kind` (llm, tool, chain), `langsmith.trace.session_id`, `langsmith.metadata.*` |
| `braintrust` | `https://api.braintrust.dev/otel/v1/traces` | `Authorization: Bearer` (`BRAINTRUST_API_KEY`), `x-bt-parent: project_id:<id>` (`BRAINTRUST_PROJECT_ID`) | protobuf | `braintrust.span_attributes.type` (task, score), `braintrust.metadata.*`; model and tool spans map from `gen_ai.operation.name` as Braintrust documents |
| `datadog` | none: pass `-endpoint` (per Datadog site) | `dd-api-key` (`DD_API_KEY`), `dd-otlp-source: llmobs`, `DD-LLMObs-Enabled: true` | protobuf | resource `dd_llmobs_enabled=true`; the GenAI attributes already written |
| `maxim` | `https://api.getmaxim.ai/v1/otel` | `x-maxim-api-key` (`MAXIM_API_KEY`), `x-maxim-repo-id` (`MAXIM_LOG_REPO_ID`) | JSON | root `maxim.metadata`: trace name, run facts as trace tags, verified success as a trace metric |
| `phoenix` | `http://localhost:6006/v1/traces` | optional `Authorization: Bearer` (`PHOENIX_API_KEY`) | protobuf | resource `openinference.project.name=heimdall`; `openinference.span.kind` (LLM, TOOL, AGENT, CHAIN, EVALUATOR, GUARDRAIL), `llm.model_name`, `llm.provider`, `llm.token_count.{prompt,completion,total}`, `tool.name`, `session.id` |
| `jaeger` | `http://localhost:4318/v1/traces` | none | JSON | none |

Attributes already on a span are never overwritten. Protobuf is encoded by hand in `export/otlppb.go` (field numbers of opentelemetry-proto `collector/trace/v1`, the same as the decoder in `agentevents/pbwire.go`), so the bench gains no dependency; a test decodes it with that independent reader and compares it with the JSON of the same trace, and Jaeger accepted it.

Verified from the documents: every URL path, header name and fixed header value in the table, the encodings Langfuse, Maxim, Datadog's intake and Jaeger accept, and every attribute name added. Not verified:

- the variable names `LANGFUSE_PUBLIC_KEY`/`LANGFUSE_SECRET_KEY` (the Langfuse page names only the Basic auth string), `LANGSMITH_PROJECT` and `MAXIM_LOG_REPO_ID` are the presets' choices;
- whether LangSmith, Braintrust (traces) and Phoenix accept OTLP/JSON: not stated, so those presets send protobuf, the OpenTelemetry exporters' default;
- the Datadog intake host, which the page fills in per site by script.

## Inspect logs: not written

`export inspect` prints why. Inspect's log format is defined by its Python classes (`EvalLog`: eval spec, plan, results, stats, samples); its JSON schema comes from the `inspect log schema` command rather than a published file, and the docs say to read and write logs through the Python API rather than as JSON. A Heimdall run is not an Inspect sample (no solver plan, no scorer), so a log would be mostly invented fields. Every Eval Ever is the interchange Inspect results are converted into, so `export eee` covers the same need.

## Tests

`go test ./export/`: Harbor goldens (`testdata/harbor/t01/`), the round trip through the importer, held-out, services, judged and withheld cases, the deterministic tar, `check.py` on JUnit, reward, exit code and answer inputs; EEE golden (`testdata/eee/arm-A.json`), schema validation and rejection of bad records, synthetic refusal; run table goldens and the `ExportRunRow` contract; protobuf against the JSON decode; every preset against a fake collector. `go test ./cmd/bifrost-bench -run TestExport` runs each subcommand on the recorded $0 smoke experiment; `go test ./handlers -run TestExportRoutes` the API. `npx vitest run components/export` the Export menu and the contract.
