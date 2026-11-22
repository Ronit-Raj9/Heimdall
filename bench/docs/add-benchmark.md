# Add a benchmark from anything

`/suites/add` (Benchmarks > Add benchmark > From a file or link), `heimdall add`, the
`/api/v1/onboard` routes and the MCP tools `inspect_source`, `draft_benchmark`,
`run_readiness` and `publish_benchmark` are one pipeline (package `bench/onboard`):

1. **Detect.** Says what the input is with no model call: CSV, JSON Lines or tickets; a
   Hugging Face dataset link; a zip or folder in a benchmark format an importer reads
   (Harbor `task.toml` folders, SWE-bench rows); a Harbor id `name@version`; a GitHub
   repository, an OpenAPI spec or an OTLP trace export (recognised, with the place that
   turns them into tasks today).
2. **Inspect.** Units, five samples (e-mail addresses and phone numbers hidden), columns,
   licence, warnings, an estimate of readiness runs and judge calls, and a suggested plan:
   benchmark id, grader, column mapping, limit and held-out share (25% for your own data,
   0% for public data). Nothing is written.
3. **Draft.** Writes the tasks into `drafts/<set>/benchmarks/<suite>/`, a full benchmark
   folder outside `benchmarks/`, so evaluations never see it. Cheap gates run at once:
   static validation, exact and near duplicates (inside the draft and against the target
   benchmark) and the held-out split. Every task shows its instruction exactly as the agent
   sees it, its checks as plain sentences read from its files, and its controls with their
   origin (from your data, or a fixed rule).
4. **Readiness** (`onboard.ready` job). The real admission: the reference passes three
   times, the empty answer, wrong answers and exploit fail. The draft shows a task by check
   matrix and each task's control catch rate. Locally the draft folder is admitted in place;
   a hosted server stages it as benchmark `draft-<set>` for the worker run and removes it.
5. **Publish** (`onboard.publish` job). Copies the accepted, ready tasks (or, on request,
   accepted tasks that are not ready) into the benchmark, private to the organization,
   stamps the leak marker on held-out tasks, appends the source and plan to the benchmark's
   `onboard.yaml`, and carries each readiness record into `suite.lock` when the published
   files and settings are exactly the ones checked; any other task is listed to check again
   on the benchmark.

```sh
export HEIMDALL_URL=https://heimdall.example.com HEIMDALL_TOKEN=hm_...
heimdall add inspect faq.csv
heimdall add faq.csv -suite support-faq -held-out 25 -publish
heimdall add https://huggingface.co/datasets/openai/gsm8k -split main/test -limit 40 -ready
heimdall add ./terminal-bench-2 -importer harbor -limit 25
heimdall add drafts | show <draft> | ready <draft> | publish <draft>
```

Harbor registry ids resolve once `onboard.HarborResolver` is set (Harbor registry lane);
until then download the dataset folder and drop it as a zip. Gated Hugging Face datasets
need a Hugging Face token saved under Settings > Secrets, passed as the connection.
