# Runner safety

What the planner, the scheduler, the janitor and the egress probe guarantee about running untrusted agents next to each other on one host, and what they do not. Code: `engine/planner.go`, `engine/scheduler.go`, `engine/janitor.go`, `engine/pipeline.go`, `gateways/bifrost/preflight.go`.

## What is guaranteed

### One live owner per slot

A slot is run only under its lease: `lease.json` in the slot folder, created with `O_EXCL` and changed only under the slot's `flock`, so the protocol is exact between processes on one host (`store/lease.go`). The owner heartbeats every 15 s; a lease whose heartbeat is older than the stale threshold (2 min) may be reclaimed by any other scheduler.

### A lost lease stops the run

The scheduler treats a heartbeat as a lost lease when the store says another owner holds the lease, when the lease is gone, or when no heartbeat has succeeded for longer than the stale threshold (from then on anyone may reclaim it). Other heartbeat errors are logged and retried.

On a lost lease the slot's context is cancelled with an `ErrLeaseLost` cause. The run's later stages stop, `cleanup` still runs on a fresh context (workspace removed, run key revoked), and the run is sealed as `aborted_infra` with a part failure (stage `leased`, part `engine`) that says `slot lease lost` and why. That holds whether or not the agent had started: another owner may already be running the slot, and two counted runs of one slot would bias the ledger. The slot gets no retry from this owner, the lease is not released (it is no longer this owner's), and the summary counts the slot in `lease_lost`.

### The janitor never removes a live run

At start every scheduler (`run`, `run -resume`, `check`, the UI launcher) sweeps leftovers. It first reads every slot lease under its runs root and under the agent check root (`checks/` in the project), for every experiment and every owner:

- A run in a slot whose lease heartbeat is fresh is **live**: its containers, volumes, networks and run key are kept, however old they are.
- A run in a slot whose lease is stale, or a sealed run in a slot that holds no lease, is **dead**: its objects are removed and its key revoked.
- Any other run has **no lease information**: a run of another project folder on the same docker host and gateway, an admission control, or an agent check (which runs without a lease). Its objects are removed only when every one of them was listed, its creation time could be read, and the youngest is older than the grace period (6 h, `engine.DefaultSweepGrace`). A run whose age cannot be read is kept.

Objects without a `bifrost-bench.run` label (the evaluation network, the gateway stack) are never touched. When a docker listing fails, only dead runs are removed and no key is revoked, since a young run the sweep could not fully see would lose its workspace or its key. When the lease files cannot be read at all, nothing is swept.

### The spend cap holds under concurrency

With `budget.total_usd` set, a worker reserves one run's estimated cost before it leases a slot, under the same lock that records costs and decides the stop:

- At or past the cap the scheduler stops leasing for good (`budget_stopped`).
- Below it, a slot starts when nothing is in flight, or when the spend so far plus every in-flight reservation is still below the cap. Otherwise the worker waits for an in-flight slot to finish.
- The estimate is the highest cost observed so far in the experiment, or `budget.reserve_usd` when that is higher. Before any positive cost is observed and with no `reserve_usd`, the estimate is the whole remaining budget, so runs go one at a time until a positive cost is known. An observed zero (a run whose agent never started) never makes the estimate zero.
- A run whose agent started but whose `cost_usd` is unknown is counted at the estimate, never as zero, on resume too. The summary reports `unknown_cost_runs`. Without `reserve_usd`, one unknown-cost run before any observed cost uses up the cap; set `reserve_usd` to a realistic per-run cost when costs may be unknown (a model missing from the price snapshot).
- A run whose agent never started made no model call, so its cost is a known zero.
- A run's budgeted cost is its `cost_usd` plus the model-played user's `simulator_cost_usd` and its judge's cost (`grade.json`); either part unknown makes the run's cost unknown. `cost_usd` itself stays agent-only for arm comparison.
- A retry (and the first new attempt after an interrupted run is recovered) starts only while the spend so far is under the cap.
- Spend charged to a remote run that was never sealed (a rejected upload, an abandoned or lost lease) is written to the remote journal with the charge and counted again on resume, once per lease.

Eight workers against a cap that allows three runs start exactly three (`TestSchedulerSpendCapConcurrent`). The cap can still be passed by at most the last admitted run's estimate plus whatever in-flight runs cost beyond their estimates.

### Only `smoke: true` relaxes the suite lock

An experiment is refused when its suite has no lock or a selected task is not admitted. `smoke: true` in the experiment relaxes that to warnings, so the synthetic smoke run works on a pack that is still being written. The name relaxes nothing: an experiment named `smoke-anything` without the field is refused like any other, and the error says to set the field. The flag is part of the experiment, so the manifest freezes it and every later reader can tell the runs are a synthetic smoke test. `agent check` relaxes the lock too (one arm, scripted model); that is a separate plan mode, not a name.

### A broken helper image never passes the egress probe

The egress probe first runs the canonical helper self-test (`bifrost.HelperSelfTest`, `--network none`): every tool the bench runs in the helper (`sh`, `cp`, `git`, `python3`, `sleep`) plus `getent`, which the probe resolves a public name with. Any missing tool makes the probe `not_run` with the tool named; it is never reported `blocked` or `open`. The probe script repeats the self-test and also recognises a shell's `<tool>: not found`, `<tool>: command not found` and docker's `exec: "<tool>": executable file not found`. `getent` exiting 2 (the name did not resolve) is the blocked answer; any other failure is no result, which makes the probe incomplete, also `not_run`. Doctor's `helper tools` row runs the same self-test.

### The manifest covers every planted file

For a fixture probe the planner hashes every overlay's source file into the manifest's per-condition files map: the single `overlay` and every entry of `overlays`, task fixtures and `{{suite.root}}` fixtures alike, keyed `<task>:<workspace path>`. A fixture appended to the agent's user-level notes (`to: {{agent.notes.user_path}}`) is keyed `<task>:append:<n>:<notes.user_path>`. Two overlays of one probe that write the same workspace path are a plan error, since only the last would reach the agent. A probe's `markers` and `paths` are literal strings in the probe YAML, which the suite lock pins by hash; the manifest records the suite lock hash.

### Existing manifests are unaffected

Lock enforcement happens at plan time; `run -resume` and the UI launcher load the frozen manifest and never re-plan, so a sealed smoke manifest without the field still resumes. `smoke` and `budget.reserve_usd` are `omitempty`, so manifests without them hash as before. Overlay hashing changes the files map only for new plans of experiments whose probes use `overlays`; experiments with single-overlay probes plan byte-identical files maps (checked on `experiments/batch-frameworks.yaml` against `docs/results/e_batch_frameworks_9z0h/manifest.json`).

## What is not guaranteed

- **One host.** Leases are files plus `flock` on a local filesystem. Two hosts sharing a runs folder (NFS, a synced folder) are not coordinated, and two hosts that share one gateway see each other's runs only as runs without lease information.
- **Other project folders and custom check folders.** The janitor reads leases only under its own runs root and `checks/`. Every per-run docker object carries a `bifrost-bench.project` label (a hash of the runs root that created it), and a janitor never removes the objects of another project, however old; that project's own janitor collects them. Objects created before the label existed, and gateway keys of another project's runs with no docker object left, are still protected by the grace period alone. An agent check run with `-runs` elsewhere in the same project is protected by the grace period alone too: one that lasts longer than 6 h can lose its workspace and key.
- **A paused process.** A scheduler stopped past the stale threshold (SIGSTOP, a suspended laptop) loses its slots. Another scheduler that reclaims such a slot seals the interrupted run itself (`infra_error` when its agent had started, then that is the slot's counted run), and the paused owner's own seal then fails. The run's work is not repeated, but it is recorded by the reclaimer, not by the owner. A process paused while it holds a slot's lock file delays other lease changes of that slot by at most 10 s: each lease call then gives up with a busy-lock error and leaves the slot to its holder, and a cancelled caller stops waiting at once.
- **Two schedulers on one experiment and the cap.** Each scheduler reserves against its own view: the sealed runs it read at start plus its own runs. Two schedulers on the same experiment at once (a `run -resume` next to the UI launcher) can together pass the cap by up to the other's in-flight spend. Run one scheduler per experiment.
- **Spend is the bench's ledger, not the provider's bill.** Costs are priced from the pinned price snapshot; a wrong or missing price makes the cap wrong or the cost unknown. Virtual key budgets are only a backstop.
- **The janitor runs at scheduler start only.** Leftovers of a crash stay until the next scheduler starts. Keys are matched to runs by name (`r_...`); a key named otherwise is never touched.
- **Egress is probed, not proven.** The probe checks DNS, a public TCP address and the cloud metadata address from the evaluation network at plan time and in doctor. It is not every destination, and the per-run network observer is separate evidence.
- **Docker is the isolation boundary.** It is not a hard security boundary against a determined attacker. The default seccomp and AppArmor profiles stay on; there is no custom profile.
- **A task can opt into a root agent.** A task with its own image and `environment.user: root` (every imported Harbor task) runs its agent, scripted controls and verifier as root, with a writable root file system and the capabilities a package install needs (`utils.RootCaps`). Privileged mode, host mounts, the docker socket and external networks stay off, and no-new-privileges stays on, but anything that breaks out of the container starts as root, not uid 1000. Every other task keeps uid 1000 with all capabilities dropped.
- **External APIs in live or record mode reach a real host.** A task may declare an external HTTP API (`docs/external-apis.md`). By default runs replay a committed cassette and nothing leaves the bench. An experiment with `external: live` or `record` gives the run's tool proxy, and only it, a route to exactly that API's host and port through a per-run forwarder on a network the agent never joins; the credential named by `auth_env` is passed to the proxy container by variable name. The agent still has no route out, every call is in the ledger and the allowlist is enforced, but what the API answers, how fast and whether it is up is outside the bench, so those experiments are labelled not repeatable and kept off the leaderboard. An API that acts on the world (sends mail, charges a card) does so for real in these modes: allow only what a run may do.
- **A published tool gate opens a port on the bench host.** A remote agent with `remote.tools: expose` (`docs/remote-execution.md`) gets the run's tool proxy published through a per-run gate container, `bench-rt-<run id>`, the only container of a run with a published port. It is off by default, binds loopback by default (`remote_tools.listen: 127.0.0.1:0`), forwards only to the run's tool proxy (which keeps no route out), and admits only requests with the run's bearer token (32 random bytes minted per run, held in memory, redacted in `plan.json`, compared by sha256 in constant time, never logged), under `/r/<run id>/`, before the run's deadline, at most 20 requests per second and 8 MiB per body. Anyone who has the token and can reach the address can call the run's tools while the run lives, including a browser or desktop service. The gate has no TLS: binding it beyond loopback without a tunnel, VPN or TLS reverse proxy sends the token and every tool call in clear text. Hidden services are never published.
- **No disk quota.** The workspace volume has no quota beyond the size watchdog; an agent can fill the host disk.
