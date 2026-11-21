# CLI and website parity

Every `bifrost-bench` (also `heimdall`) command and where to do the same on
the website. Derived from the command table in `cmd/bifrost-bench/main.go`
and the UI routes in `ui/app`. Status: **web** (fully on the website),
**partial** (main path on the website, some subcommands CLI only),
**CLI only** (with the reason).

Nav groups: Benchmark, Build, Production, Govern, Operate. "Experiment page"
means `/experiments/<id>`; its tabs are named in the table.

| CLI command | Website location (page, button) | Status | Notes |
|---|---|---|---|
| `agent init` | Build > Agents, **New agent** (`/agents/new`) | web | templates by category |
| `agent versions` / `agent snapshot` | Agent page, **Versions** tab (every save makes a version) | web | with `-server` calls `/api/bench/agents/{id}/versions`; local mode reads and writes `.heimdall/agent-versions/<id>/` |
| `agent build` | Agent page, Checks tab, **Build** (job `agent.build`) | partial | local servers only; hosted orgs use prebuilt or admin-approved custom images, because builds would run Docker in the server |
| `agent check` | Agent page, Checks tab, **Check** (job `agent.check`) | web | hosted: runs as a tiny experiment on a runner |
| `audit` | Experiment page, Analysis tab, **Audit a published table** | web | CSV upload or paste |
| `awareness` | Experiment page, Analysis tab, **Eval awareness** (job `awareness.run`) | web | |
| `bifrost cache-replays` | none | CLI only | gateway feature report not wired to a page yet; run it and read the output |
| `blame` | Experiment page, Analysis tab, **Blame** (job `blame.run`); run page agents panel | web | tier 2 replay is opt-in |
| `canary init` / `list` / `status` | Production > Drift canaries, **Create canary** | web | |
| `canary baseline` / `run` | Drift canaries, baseline and **Run** buttons (jobs `canary.baseline`, `canary.run`) | web | hosted: run goes to a runner |
| `canary simulate` | none | CLI only | offline what-if over sealed history, not wired yet |
| `check` | Experiment page, Analysis tab, **Re-check verdict** (job `check.run`) | web | exit codes become the job result |
| `ci install github` | Operate > Integrations, CI tab, **Open pull request** | web | needs Settings > GitHub connected |
| `ci card` | none | CLI only | runs inside the CI workflow on the PR, by design |
| `clusters` | Production > Failure inbox (clusters view); Analysis tab, **Failure clusters** | web | |
| `codescan diff` | Experiment page, Analysis tab, **Code scan** (job `codescan.run`) | web | |
| `codescan rules` | code scan panel lists the rules that fired | partial | full rule list only in the CLI |
| `compare --a/--b` (agents or models) | Benchmark > Runs (`/compare`), **Compare agents or models**; QuickRun | web | |
| `compare --baseline/--candidate` (git refs) | Integrations, CI tab, **Live** mode | partial | needs a checkout of your repo; on the web it runs per pull request through CI, or run it locally and Upload the sealed runs |
| `compliance report` | Govern > Releases, **Write compliance pack** (job `release.compliance`) | web | |
| `cost explain` / `cost pareto` | Experiment page, Cost tab; Leaderboard pareto view | web | |
| `db migrate` / `status` / `backup` | Operate > Operations, Evidence, **Back up** | partial | migrate and status run at server start; operator shell task |
| `doctor` | Operate > Operations, Health, **Run doctor** (job `operate.doctor`) | web | |
| `estimate` | Experiments > New experiment, **Estimate** | web | |
| `eval run` | **Evaluate your agent** (`/evaluate`) wizard, **Launch**; estimate shows live in step 4 | web | `--agent codex@v2 [--vs agent@vM \| --vs-model M] --model M --pack coding \| --benchmarks a,b --tasks 5 --trials 2 --faults model-down,broken-tool [--estimate]`; with `-server` (or `HEIMDALL_SERVER`) it calls `POST /api/bench/evaluations`; local mode only records the evaluation, launching needs a server |
| `eval list` / `show` / `cancel` | Evaluations (`/evaluations`), evaluation page (`/evaluations/<id>`), **Cancel** | web | |
| `eval report` | Evaluation page (`/evaluations/<id>`), report card; agent page, **Report card** tab | web | prints a capability table, one row per benchmark with the A/B verdict, faults and safety; `--json` gives the API shape. Local mode reads a saved `<id>.report.json` only |
| `eventcollector` | none | CLI only | internal per-run process the engine starts; not a user action |
| `evidence verify` / `mirror` / `archive` / `restore` / `backup` | Operate > Operations, Evidence buttons | web | |
| `evidence list` / `fetch-backup` | none | CLI only | operator shell tasks on the mirror |
| `export` | Operate > Integrations, Exports tab, **Export**; Experiment page, Export tab; run page **Download trace (OTLP)** | web | |
| `extend` | Experiment page, Actions tab, **Extend** (job `experiment.extend`) | web | |
| `gpu check` | Operate > Operations, Health, **GPU check** (job `gpu.check`) | partial | local servers only; hosted GPU fit is checked by the runner |
| `hookfault` | none | CLI only | internal per-run fault controller |
| `import` | Build > Catalog, **Import**, **Start import** (also `/imports`) | web | data path must be on the server |
| `inbox list` / `show` / `accept` / `reject` / `snooze` | Production > Failure inbox | web | |
| `inbox ingest` | Failure inbox, **Push failures in** (webhook) | web | |
| `inbox pull` | Failure inbox, **Pull now** (job `inbox.pull`) | partial | local servers only: it reads trace tools with a credential; hosted orgs push instead |
| `index rebuild` | Experiment page, Analysis tab, **Results index** (job `index.rebuild`) | web | |
| `index status` / `query` | Benchmark > Leaderboard, trends (`/trends`) | partial | free-form query only in the CLI |
| `init` (project or repo) | Suites > **New suite**, Agents > **New agent**, Files | partial | repo detection (tests, agent CLIs, ports) needs a local checkout |
| `judge calibrate` / `label` / `status` | Suites, Calibration (`/calibration`) | web | |
| `k8s` | Operate > Operations, **Kubernetes status** (job `k8s.status`) | partial | gateway, image load and cleanup are cluster operator tasks |
| `leaderboard` | Benchmark > Leaderboard | web | |
| `livescore init` / `pull` / `compare` / `status` | Production > Live traffic, **Set up live scoring**, **Pull now**, **Recompute comparison** | web | |
| `matrix` | Experiment page, Matrix tab (three or more arms) | web | |
| `mcp scan` | Agent page, Tools tab (MCP section) | partial | servers are edited on the web; the sandboxed admission scan is CLI only for now |
| `migrate` / `migrate report` | Experiment page, Actions tab, **Compare models** (job `experiment.migrate`); Migration tab | web | |
| `monitor certify` / `list` / `show` | Govern > Monitors, **Certify a monitor** (job `monitor.certify`) | web | |
| `org create` | sign-up creates an org (Continue with GitHub or Create one) | web | |
| `org list` / `member add` | Settings > Members, **Invite someone**; Admin > Organizations | web | |
| `org import` | Operate > Upload, Sealed runs | web | |
| `plan` | Experiments > New experiment, **Plan** | web | |
| `proxy` | none | CLI only | the fault proxy is a server-side service, started by the stack |
| `pull` / `push` | Build > Files, **Download project (.zip)**; Operate > Upload | partial | push and pull sync a local folder, so they are CLI by nature; see [web-sync.md](web-sync.md) |
| `regate` | Experiment page header, **Regrade** (job `experiment.regate`); the Verdict tab's grade picker reads a regrade | web | |
| `release create` / `list` | Govern > Releases (release panel); verdict **Release** | web | |
| `release verify` | Releases, **Verify chain and evidence** (job `release.verify`) | web | |
| `release export` | Releases, Write compliance pack; Files download | partial | raw log export only in the CLI |
| `review list` / `show` / `add` / `log` | Govern > Review queue; run page review panel | web | |
| `run` | Experiments > New experiment, **Run experiment**; QuickRun **Start run**; welcome wizard **Run benchmark** | web | runs execute on a runner |
| `runner start` / `worker` | Settings > Runners, **Create runner token** shows the command | CLI only | a runner is a process on your own machine; the page gives the exact command and a one-line installer |
| `runner status` | Operate > Runners; Settings > Runners table | web | |
| `schedule add` / `list` / `remove` / `run-now` | Production > Schedules, **Add schedule**, **Run now** | web | |
| `schedule daemon` | none | CLI only | the server runs schedules itself; daemon is for CLI-only setups |
| `serve` | none | CLI only | starts the website itself |
| `simulator spread` | Operate > Operations, **Simulator spread** (job `simulator.spread`) | web | |
| `skill scan` | Agent page, Tools tab, skill upload (scanned on upload) | web | |
| `skill rules` | none | CLI only | rule list reference |
| `suite init` | Build > Suites, **New suite** | web | |
| `suite lint` / `lock` | Suite page, Edit tab, **Lint**, **Lock** (jobs `suite.lint`, `suite.lock`) | web | |
| `suite admit` | Suite page, Ops tab, **Admit all tasks** (job `suite.admit`) | web | hosted: runs on a runner |
| `task init` (from a commit) | Suite page, Ops tab, **Task from a commit** (job `task.from-commit`) | web | needs GitHub connection or a local folder |
| `task validate` / `admit` | Suite page, Ops tab, Lint and **Admit all tasks** | web | |
| `tasks mine` | Suite page, Ops tab, **Mine tasks** (job `tasks.mine`) | web | |
| `toolproxy` | none | CLI only | internal per-run process |
| `user create` / `set-password` | sign-up page; Settings > Password; Admin > Users | web | |
| `version` | none | CLI only | `GET /api/bench/health` reports the server build; no page shows it yet |
| `visual render` / `capture` / `compare` | Operate > Operations, Suite checks, **Visual compare**, capture (jobs `visual.compare`, `visual.capture`) | partial | `render` is CLI only |
| `voice check` | Operate > Operations, Suite checks (job `voice.check`) | web | |

## Why the CLI-only rows stay CLI only

- Internal processes (`proxy`, `hookfault`, `eventcollector`, `toolproxy`):
  the engine starts them per run. There is nothing for a user to press.
- Machine-local by nature (`runner start`, `push`, `pull`, `serve`, repo
  `init`, `ci card`): they act on a machine or a checkout the website cannot
  reach. The website shows the exact command where one is needed.
- Not wired yet (`bifrost cache-replays`, `canary simulate`, `mcp scan`,
  `skill rules`, `codescan rules`, `index query`, `release export`,
  `visual render`): reports and references with no page so far. Each still
  works from the CLI against the same project files.
