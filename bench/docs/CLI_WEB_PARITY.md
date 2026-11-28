# CLI and website parity

Every `bifrost-bench` (also `heimdall`) command and where to do the same on
the website. Derived from the command table in `cmd/bifrost-bench/main.go`
and the UI routes in `ui/app`. Status: **web** (fully on the website),
**partial** (main path on the website, some subcommands CLI only),
**CLI only** (with the reason).

Sidebar groups: Test (Agents, Evaluations, Comparisons, Failure tests, MCP
server test), Results (Runs, Failures, Leaderboard, Releases), Benchmarks
(Benchmarks, Human review), Production (Live scoring, Checks, Gateway, Safety
rule tests, Auto-improve, Settings). "Comparison page" means
`/experiments/<id>`; its tabs are named in the table.

| CLI command | Website location (page, button) | Status | Notes |
|---|---|---|---|
| `add` | Benchmarks, **Add a benchmark** (`/suites/add`) | web | file, folder, URL or Harbor id |
| `admin grant` / `revoke` / `list` | none | CLI only | operator task on the server database; hosted servers also read `HEIMDALL_SERVER_ADMINS` |
| `agent build` | Agent page, Checks tab, **Build** (job `agent.build`) | partial | local servers only; hosted orgs use prebuilt or admin-approved custom images, because builds would run Docker in the server |
| `agent check` | Agent page, Checks tab, **Check** (job `agent.check`) | web | hosted: runs as a tiny experiment on a runner |
| `agent init` | Agents, **New agent** (`/agents/new`) | web | templates by category |
| `agent versions` / `agent snapshot` | Agent page, **Versions** tab (every save makes a version) | web | with `-server` calls `/api/bench/agents/{id}/versions`; local mode reads and writes `.heimdall/agent-versions/<id>/` |
| `audit` | Comparison page, Analysis tab, **Audit a published table** | web | CSV upload or paste |
| `awareness` | Comparison page, Analysis tab, **Eval awareness** (job `awareness.run`) | web | |
| `bifrost cache-ab` | none | CLI only | prompt cache on and off over one experiment; no page yet |
| `bifrost cache-replays` | none | CLI only | gateway feature report not wired to a page yet; run it and read the output |
| `blame` | Comparison page, Analysis tab, **Blame** (job `blame.run`); run page agents panel | web | tier 2 replay is opt-in |
| `canary baseline` / `run` | Checks, baseline and **Run** buttons (jobs `canary.baseline`, `canary.run`) | web | hosted: run goes to a runner |
| `canary init` / `list` / `status` | Checks, **Create canary** | web | |
| `canary simulate` | none | CLI only | offline what-if over sealed history, not wired yet |
| `check` | Comparison page, Analysis tab, **Re-check verdict** (job `check.run`) | web | exit codes become the job result |
| `ci card` | none | CLI only | runs inside the CI workflow on the PR, by design |
| `ci evaluate` | none | CLI only | runs inside the CI workflow, by design |
| `ci install github` | Settings > Integrations, CI tab, **Open pull request** | web | needs Settings > GitHub connected |
| `clusters` | Failures (clusters view); Analysis tab, **Failure clusters** | web | |
| `codescan diff` | Comparison page, Analysis tab, **Code scan** (job `codescan.run`) | web | |
| `codescan rules` | code scan panel lists the rules that fired | partial | full rule list only in the CLI |
| `compare --a/--b` (agents or models) | Runs > Compare (`/compare`), **Compare agents or models**; QuickRun | web | |
| `compare --baseline/--candidate` (git refs) | Integrations, CI tab, **Live** mode | partial | needs a checkout of your repo; on the web it runs per pull request through CI, or run it locally and Upload the sealed runs |
| `compliance report` | Releases, **Write compliance pack** (job `release.compliance`) | web | |
| `connect` | Agents, **Connect** (`/connect`) | web | |
| `cost explain` / `cost pareto` | Comparison page, Cost tab; Leaderboard pareto view | web | |
| `db migrate` / `status` / `backup` | Settings > Operations, Evidence, **Back up** | partial | migrate and status run at server start; operator shell task |
| `doctor` | Settings > Operations, Health, **Run doctor** (job `operate.doctor`) | web | |
| `drain` | none | CLI only | operator task before a restart; the comparison's Pause is a different thing |
| `egress serve` / `record` / `image` / `show` | none | CLI only | recorded internet for offline tasks; author and operator task |
| `estimate` | Comparisons > **New comparison**, **Estimate** | web | |
| `eval list` / `show` / `cancel` | Evaluations (`/evaluations`), evaluation page (`/evaluations/<id>`), **Cancel** | web | |
| `eval report` | Evaluation page (`/evaluations/<id>`), report card; agent page, **Report card** tab | web | prints a capability table, one row per benchmark with the A/B verdict, faults and safety; `--json` gives the API shape. Local mode reads a saved `<id>.report.json` only |
| `eval run` | **Evaluate your agent** (`/evaluate`) wizard, **Launch**; estimate shows live in step 4 | web | `--agent codex@v2 [--vs agent@vM \| --vs-model M \| --mode model_switch --models M2,M3] --model M --pack coding \| --benchmarks a,b --tasks 5 --trials 2 --faults model-down,broken-tool [--estimate]`; with `-server` (or `HEIMDALL_SERVER`) it calls `POST /api/bench/evaluations`; local mode only records the evaluation, launching needs a server |
| `eventcollector` | none | CLI only | internal per-run process the engine starts; not a user action |
| `evidence list` / `fetch-backup` | none | CLI only | operator shell tasks on the mirror |
| `evidence verify` / `mirror` / `archive` / `restore` / `backup` | Settings > Operations, Evidence buttons | web | |
| `export` | Settings > Integrations, Exports tab, **Export**; Comparison page, Export tab; run page **Download trace (OTLP)** | web | |
| `extend` | Comparison page, Actions tab, **Extend** (job `experiment.extend`) | web | |
| `first-look` | Agents, **Connect** (`/connect`), first look | web | |
| `gpu check` | Settings > Operations, Health, **GPU check** (job `gpu.check`) | partial | local servers only; hosted GPU fit is checked by the runner |
| `hookfault` | none | CLI only | internal per-run fault controller |
| `images` / `registry` | none | CLI only | operator image tasks; registry logins for agents are in Settings |
| `import` | Benchmarks, Catalog tab, **Import**, **Start import** (also `/imports`) | web | data path must be on the server |
| `inbox ingest` | Failures, **Push failures in** (webhook) | web | |
| `inbox list` / `show` / `accept` / `reject` / `snooze` | Failures | web | |
| `inbox pull` | Failures, **Pull now** (job `inbox.pull`) | partial | local servers only: it reads trace tools with a credential; hosted orgs push instead |
| `index rebuild` | Comparison page, Analysis tab, **Results index** (job `index.rebuild`) | web | |
| `index status` / `query` | Leaderboard, trends (`/trends`) | partial | free-form query only in the CLI |
| `init` (project or repo) | Benchmarks > **New suite**, Agents > **New agent**, Files | partial | repo detection (tests, agent CLIs, ports) needs a local checkout |
| `issuebench work` | Benchmark page, Build tab, **From issues** | web | `work` is the runner side |
| `judge calibrate` / `label` / `status` | Benchmarks, Calibration (`/calibration`) | web | |
| `k8s` | Settings > Operations, **Kubernetes status** (job `k8s.status`) | partial | gateway, image load and cleanup are cluster operator tasks |
| `leaderboard` | Leaderboard | web | |
| `livescore init` / `pull` / `compare` / `status` | Live scoring, **Set up live scoring**, **Pull now**, **Recompute comparison** | web | |
| `login` / `logout` | device code approval page | CLI only | machine-local by nature |
| `matrix` | Comparison page, Matrix tab (three or more arms) | web | |
| `mcp scan` | Agent page, Tools tab (MCP section) | partial | servers are edited on the web; the sandboxed admission scan is CLI only for now |
| `migrate` / `migrate report` | Comparison page, Actions tab, **Compare models** (job `experiment.migrate`); Migration tab | web | |
| `mock fixtures` | none | CLI only | record and replay of a task service |
| `mock openapi` | Benchmark page, Build tab, **Your API (OpenAPI)** | web | |
| `monitor certify` / `list` / `show` | Safety rule tests, **Certify a monitor** (job `monitor.certify`) | web | |
| `org create` | sign-up creates an org (Continue with GitHub or Create one) | web | |
| `org import` | Benchmarks, **Upload** (`/upload`), Sealed runs | web | |
| `org list` / `member add` | Settings > Members, **Invite someone**; Settings > Server admin, Organizations | web | |
| `paired` | none | CLI only | paired realism or framing test of one arm across two experiments |
| `plan` | Comparisons > **New comparison**, **Plan** | web | |
| `proxy` | none | CLI only | the fault proxy is a server-side service, started by the stack |
| `pull` / `push` | Settings > Files, **Download project (.zip)**; Benchmarks, **Upload** (`/upload`) | partial | push and pull sync a local folder, so they are CLI by nature; see [web-sync.md](web-sync.md) |
| `reference import` / `publish` / `unpublish` | none | CLI only | operator task |
| `reference list` | Reference board (`/reference`) | web | read only |
| `regate` | Comparison page header, **Regrade** (job `experiment.regate`); the Verdict tab's grade picker reads a regrade | web | |
| `release create` / `list` | Releases (release panel); verdict **Release** | web | |
| `release export` | Releases, Write compliance pack; Files download | partial | raw log export only in the CLI |
| `release verify` | Releases, **Verify chain and evidence** (job `release.verify`) | web | |
| `review list` / `show` / `add` / `log` | Human review; run page review panel | web | |
| `run` | Comparisons > **New comparison**, **Run experiment**; QuickRun **Start run**; welcome wizard **Run benchmark** | web | runs execute on a runner |
| `runner start` / `worker` | Settings > Workers, **Create runner token** shows the command | CLI only | a runner is a process on your own machine; the page gives the exact command and a one-line installer |
| `runner status` | Settings > Workers; Settings > Workers table | web | |
| `schedule add` / `list` / `remove` / `run-now` | Checks, **Add schedule**, **Run now** | web | |
| `schedule daemon` | none | CLI only | the server runs schedules itself; daemon is for CLI-only setups |
| `serve` | none | CLI only | starts the website itself |
| `serve <target>` | Connect page gives the command | CLI only | serves your agent to Heimdall from your machine |
| `simulator spread` | Settings > Operations, **Simulator spread** (job `simulator.spread`) | web | |
| `skill rules` | none | CLI only | rule list reference |
| `skill scan` | Agent page, Tools tab, skill upload (scanned on upload) | web | |
| `status incident list` | Status page (`/status`) | web | read only |
| `status incident open` / `update` / `resolve` / `remove` | none | CLI only | operator task; no write route yet |
| `suite admit` | Suite page, Ops tab, **Admit all tasks** (job `suite.admit`) | web | hosted: runs on a runner |
| `suite init` | Benchmarks, **New suite** | web | |
| `suite lint` / `lock` | Suite page, Edit tab, **Lint**, **Lock** (jobs `suite.lint`, `suite.lock`) | web | |
| `task init` (from a commit) | Suite page, Ops tab, **Task from a commit** (job `task.from-commit`) | web | needs GitHub connection or a local folder |
| `task validate` / `admit` | Suite page, Ops tab, Lint and **Admit all tasks** | web | |
| `tasks mine` | Suite page, Ops tab, **Mine tasks** (job `tasks.mine`) | web | |
| `toolproxy` | none | CLI only | internal per-run process |
| `user create` / `set-password` | sign-up page; Settings > Password; Settings > Server admin, Users | web | |
| `version` | none | CLI only | `GET /api/bench/health` reports the server build; no page shows it yet |
| `visual render` / `capture` / `compare` | Settings > Operations, Suite checks, **Visual compare**, capture (jobs `visual.compare`, `visual.capture`) | partial | `render` is CLI only |
| `voice check` | Settings > Operations, Suite checks (job `voice.check`) | web | |

## Why the CLI-only rows stay CLI only

- Internal processes (`proxy`, `hookfault`, `eventcollector`, `toolproxy`):
  the engine starts them per run. There is nothing for a user to press.
- Machine-local by nature (`runner start`, `push`, `pull`, `serve`, repo
  `init`, `ci card`, `ci evaluate`, `login`, `logout`): they act on a machine or a checkout the website cannot
  reach. The website shows the exact command where one is needed.
- Operator tasks (`admin`, `drain`, `images`, `registry`, `egress`,
  `reference` writes, `status incident` writes, `db`): shell tasks on the
  server, not user actions.
- Not wired yet (`bifrost cache-replays`, `bifrost cache-ab`, `paired`, `mock fixtures`, `canary simulate`, `mcp scan`,
  `skill rules`, `codescan rules`, `index query`, `release export`,
  `visual render`): reports and references with no page so far. Each still
  works from the CLI against the same project files.
