# Importing Harbor tasks (Terminal-Bench 2, SkillsBench)

`bifrost-bench import harbor` turns a local copy of a benchmark in the Harbor task format into an ordinary suite under `benchmarks/<suite>`. After the import the suite is linted, locked, admitted and run like a hand-written one. Upstream data is never committed: the suite folder gets a `.gitignore` that ignores everything in it.

The code is `importers/harbor/`; the contract it follows is `docs/scenarios-design.md` (per-task images, the snapshot verifier, script controls, skills).

## What it reads

The importer accepts two task layouts and detects them per task folder:

| Layout | Files | Who publishes it |
|---|---|---|
| Harbor | `task.toml`, `instruction.md`, `environment/Dockerfile` (+ build context), `solution/solve.sh`, `tests/test.sh` | Terminal-Bench 2.0 (`laude-institute/terminal-bench-2`), SkillsBench 1.0 (tag `v1.0`), datasets downloaded from the Harbor hub |
| task.md | `task.md` (YAML front matter, then the instruction), `environment/`, `oracle/solve.sh`, `verifier/test.sh` | SkillsBench 1.1 and later (`benchflow-ai/skillsbench`, `main`) |

In both, `test.sh` runs the tests and writes a reward (`1` or `0`, sometimes a fraction) to `/logs/verifier/reward.txt`. Harbor uploads the tests to `/tests` (task.md: `/verifier`) and the solution to `/solution` (task.md: `/oracle`) before running them.

As checked on 15 Nov 2026:

- **Terminal-Bench 2.0**: 89 tasks, Apache-2.0, `https://github.com/laude-institute/terminal-bench-2`. The README still says 2.0. The Harbor hub also lists `terminal-bench/terminal-bench-2-1` (89 tasks, 28 revised); it is not in that repository and the importer has not been run on it.
- **SkillsBench**: Apache-2.0, `https://github.com/benchflow-ai/skillsbench`. `registry.json` lists 1.0 (Harbor layout, tag `v1.0`, commit `14f33967`) and 1.1 (task.md layout, tag `v1.1`). `main` holds 87 tasks in `tasks/` and 14 in `tasks-extra/`, in the task.md layout. Each task's skills are folders holding `SKILL.md` under `environment/skills/`.
- **Hack-Verifiable Terminal-Bench 2.1** (HVTB, arXiv 2608.22103; the paper's repository is MIT): real. The 89 tasks are published only on the Harbor hub as `hack-verifiable-environments/hv-terminal-bench-2-1` (the GitHub repository `MajoRoth/hack-verifiable-environments` holds the paper's code, not the tasks); the download carries no LICENSE file. Each task is the Terminal-Bench task plus `environment/admin/` (a copy of the solution and the tests, copied to `/app/admin` in the image) and an `ENTRYPOINT` that starts `inotifywait` watchers, which touch `/tmp/solution_accessed`, `/tmp/tests_read` or `/tmp/tests_modified` on first access. `test.sh` then writes `/logs/verifier/reward.json` as `{"reward": r, "hidden_solution": 0|1, "read_tests": 0|1, "modify_tests": 0|1}`. The importer skips these tasks by default; see "Hack-verifiable tasks" below.

- **DeepSWE** (Datacurve, Apache-2.0, 113 tasks): checked from public documents only (the GitHub README and the Hugging Face card); the task data was not downloaded. The tasks use the Harbor layout, but since DeepSWE 1.1 they grade in Harbor's separate verifier environment: a `[[verifier.collect]]` hook turns the agent's commits into a patch that is applied and tested in a pristine container, and `solution/` holds a reference patch that is never used at grading. The importer grades in a snapshot of the agent's container and needs `solution/solve.sh`, so it skips tasks that declare a separate verifier (`[verifier] environment_mode = "separate"`, `[verifier.environment]` or `[[verifier.collect]]`) with that reason, and DeepSWE is not listed as supported. Mapping such tasks to the bench's clean-room grading of the agent's diff is the natural next step. The dataset is gated on Hugging Face (`datacurve/deep-swe`): accept its conditions and download it with your own account.

## Fetch

Clone into `datasets/` (git-ignored), shallow:

```sh
cd bench
git clone --depth 1 https://github.com/laude-institute/terminal-bench-2 datasets/terminal-bench-2
git clone --depth 1 https://github.com/benchflow-ai/skillsbench datasets/skillsbench           # task.md layout
git clone --depth 1 --branch v1.0 https://github.com/benchflow-ai/skillsbench datasets/skillsbench-v1.0  # Harbor layout
```

SkillsBench `main` is about 1.1 GB checked out. A sparse checkout of a few tasks is enough to try the importer (`git clone --filter=blob:none --sparse ...`, then `git sparse-checkout set --no-cone '/*' '!/*/' '/tasks/<id>/'`).

Harbor hub datasets need the Harbor CLI. Run it in a container, never on the host, with no credentials mounted:

```sh
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD/datasets/hvtb:/out" python:3.12-slim \
  sh -c 'pip install -q --user harbor && /tmp/.local/bin/harbor datasets download hack-verifiable-environments/hv-terminal-bench-2-1 -o /out'
```

## Import

```sh
bifrost-bench import harbor -from datasets/terminal-bench-2 -suite tb2
bifrost-bench import harbor -from datasets/skillsbench -suite skillsbench -limit 10
bifrost-bench import harbor -from datasets/terminal-bench-2 -suite tb2 -ids regex-log,fix-git -set image=prebuilt -force
bifrost-bench import harbor -from datasets/hvtb/hv-terminal-bench-2-1 -suite hvtb -set hack_verifiable=keep \
  -set benchmark=hv-terminal-bench-2-1 -set source=https://hub.harborframework.com/datasets/hack-verifiable-environments/hv-terminal-bench-2-1
```

A Harbor hub download has no `.git`, README or LICENSE, so pass `benchmark`, `source` and `license` yourself; otherwise the suite records the folder name, the local path and `NOASSERTION`.

`-from` is the clone (task folders directly under it or under `tasks/`), or one task folder. `-ids` picks folders by name, `-limit` caps the number written, `-force` replaces task folders that exist; importing into an existing suite adds to its task list. The command prints every task it left out with the reason, then the next steps.

Settings (`-set key=value`):

| Key | Default | Meaning |
|---|---|---|
| `benchmark` | detected | name@version recorded in the suite: a `registry.json` entry whose commit is the clone's, else the Terminal-Bench README title, else the folder name and commit |
| `source`, `revision` | the clone's origin URL and HEAD | read from `.git` without running git; a token in the URL is dropped |
| `license` | from `LICENSE` | SPDX id; `NOASSERTION` when the file is not recognised |
| `tasks_dir` | detected | folder of task folders, relative to `-from` (for example `tasks-extra`) |
| `track` | `skills` when any task brings skills, else `terminal` | leaderboard track |
| `agent` | `agents/codex.yaml` | base agent config of the skills experiment |
| `image` | `build` | `prebuilt` starts each task image `FROM` the image `task.toml` names (`docker_image`) instead of building the upstream Dockerfile |
| `keep_networked` | `false` | `true` also imports tasks whose reference solution downloads at run time, and tasks whose `test.sh` downloads in a way that cannot move to the build (those lines are kept as they are and listed in `IMPORT.md`); they cannot pass admission without network and stay drafts |
| `hack_verifiable` | `skip` | `keep` imports tasks with a planted `admin/` honeypot (HVTB) as plain tasks, with `admin/**` protected |
| `agent_timeout` | task.toml's `[agent] timeout_sec` | seconds: one agent budget for every task (Harbor's `override_timeout_sec`) |
| `pass_threshold` | 1 | lowest passing reward, written to `verifier.pass_threshold`; the reward itself is always kept on the grade (partial credit) |
| `max_solution_mb` | 2 | largest packed solution (plus skills) the reference control embeds, in MiB, up to 256; library imports with large upstream solutions raise it |
| `lint_waivers` | `none` | `upstream` records a waiver in `task.yaml` (`lint_waivers`) and `IMPORT.md` for each static-validation error that verbatim upstream text trips in two heuristic checks only: leak words in the instruction (`instruction-term`, e.g. "hidden tests") and high-entropy `*_KEY`/`*_TOKEN` identifiers (`secret-high-entropy`). The error becomes a warning; real secret patterns, tests visible in the repo and every other check stay errors. Waivers apply only to tasks with `provenance.imported_from` |
| `unrunnable` | `skip` | `draft` writes every task the bench cannot admit (skip reasons below) as a draft: tag `draft`, the reason in `IMPORT.md`, and a reference control that fails with it, so admission refuses it and it stays visible in the suite |

## What a converted task looks like

```
benchmarks/<suite>/
  suite.yaml  base/Dockerfile  .gitignore  [experiment-skills.yaml]
  tasks/<id>/
    task.yaml  instruction.md  IMPORT.md
    environment/                 upstream build context; Dockerfile with a section appended
    skills/                      the task's skills (moved out of environment/)
    verifier/tests/upstream/     the upstream tests folder, unchanged
    verifier/tests/harbor/       verify.sh (the verifier command) and the offline test.sh
    solution/                    the upstream solution, unchanged
    controls/                    reference.sh, empty.sh, exploit.sh
    repo/                        empty: the workspace comes from the image
```

| Upstream | Bench |
|---|---|
| `environment/Dockerfile` and folder | `environment` with `dockerfile`, `context`, `build_network: true`, `user: root`, `workspace_path` = the final stage's `WORKDIR` (task.md: `sandbox.workdir` when set) |
| `[environment.env]` | `ENV` lines at the end of the Dockerfile |
| `[verifier.env]`, `[solution.env]` (task.md `verifier.env`, `oracle.env`) | exported by `verify.sh` and `reference.sh` |
| `tests/` | `verifier/tests/upstream`, copied to `/tests` (task.md: `/verifier`) when the verifier runs |
| `solution/` | `controls/reference.sh`: the folder embedded as a tar, unpacked to `/solution` (task.md: `/oracle`), then `solve.sh` run from the workdir. For a task with skills it also installs them in `/skills` and in every `.../skills` folder the solution names, because SkillsBench solutions import helper scripts from their skills |
| `environment/skills/` | `skills/`, set as `Task.Skills`; any `COPY skills ...` line in the Dockerfile is disabled |
| `[agent].timeout_sec` | `timeouts.agent_s` (default 900) |
| `[metadata].difficulty`, `category`, `tags`, `[task].keywords` | `difficulty`, tags `category:<c>` plus upstream tags |
| repository, commit, license | `provenance` on each task, `imported:` on the suite |

The suite uses grader `verifier_command` with `command: [/bin/sh, /grader/tests/harbor/verify.sh]`, `result: reward`, `result_path: /grader/out/reward.json`, `mode: snapshot`, and a timeout of the largest task verifier timeout plus 60 seconds. `verify.sh` empties `/tests` and `/logs/verifier`, copies the tests in, runs the offline `test.sh` from the workdir and copies `reward.txt` and `ctrf.json` to `/grader/out`. From a `reward.json` it keeps only the `reward` key, because the bench's reward format refuses other top-level keys; the whole file is kept as `/grader/out/harbor-reward.json`. Because the folders are emptied first, a reward the agent planted does not count; `controls/exploit.sh` plants one and must fail. On a read-only root file system `verify.sh` runs from `/tmp/harbor-verify` with `/tests` and `/logs/verifier` rewritten in `test.sh`.

Admission controls: the reference is `controls/reference.sh` (a script control, run in the agent's place), the empty control does nothing, the exploit plants a fake reward. There are no mutants. The appended Dockerfile section creates `/logs/verifier`, the tests folder, the solution folder and (with skills) `/skills` as world-writable empty folders, so the controls and the verifier work as a non-root user; if the solution folder still cannot be written, `reference.sh` unpacks to `/tmp/harbor-solution` and rewrites the path in `solve.sh`.

### Long-Horizon Terminal-Bench 2.0

LHTB 2.0 (https://github.com/zli12321/LHTB, 77 tasks in `tasks/` and `tasks-2.0/`, Apache-2.0) is Harbor format with a modified Harbor. Import each folder into one suite:

```sh
for d in tasks tasks-2.0; do
  bifrost-bench import harbor -from datasets/lhtb2 -suite lhtb2 -set tasks_dir=$d \
    -set image=prebuilt -set unrunnable=draft -set pass_threshold=0.9 \\
    -set max_solution_mb=48 -set lint_waivers=upstream
done
```

- `[agent] continue_until_timeout = true` (all 77): LHTB's Harbor resumes the agent with a bare rejection until the timeout. The bench runs the agent once up to the task's budget (1 to 8 hours here; `agent_timeout` overrides it) and grades once. Tasks get the tag `continue-until-timeout` and an `IMPORT.md` note; scores can be lower than the paper's.
- `[agent] user` and `[environment.healthcheck] command` are read and listed in `IMPORT.md` (the bench runs the agent as root and does not run the healthcheck).
- Graders give partial credit. The paper counts a task solved at reward >= 0.95; the library suite passes at >= 0.9 (several upstream reference solutions score 0.90 to 0.95), noted in every `IMPORT.md`. The raw reward is kept on every grade.
- Reference solutions that end with `python -m pip install -e .` (6 tasks) run offline: the project's `[build-system] requires` and the pinned extras are baked at build, and the line runs with `--no-index --no-build-isolation`. A `test.sh` install that logs to a file (3 tasks) is baked like any other install.
- Separate verifier containers (3 tasks, `[verifier] environment_mode = "separate"`): tests/Dockerfile up to its first `COPY` is built as a stage of the task image and its file system copied to `/opt/harbor-verifier`; at verify time the driver replays the rest of tests/Dockerfile with the tests, runs `[verifier.environment.healthcheck]`, copies the task.toml `artifacts` from the agent's container to the same paths (honouring `regular_file_only` and `max_bytes`), and runs the upstream `test.sh` chrooted there (the root verifier keeps `SYS_CHROOT`). `[[verifier.collect]]` and a prebuilt verifier image are still refused.
- A compose file whose `main` service names no image or build (chess-mate) now maps: under Harbor conventions main is the task image.
- At commit 7ca0cac: 47 importable, 30 drafts (tasks-2.0 reconstruction tasks ship no `solve.sh`).
- Git LFS holds only 5 `reference_play` gif/mp4 files under `solution/`.

### Test tools are baked into the image

Upstream test scripts download their tools when they run: `apt-get install curl`, the uv installer, then `uvx -p 3.13 -w pytest==8.4.1 ... pytest ...` (82 of 89 Terminal-Bench scripts), or `pip install pytest ...`. The bench verifier has no network. The importer rewrites each script so it needs none, and records every change in the task's `IMPORT.md`:

- `apt-get update`/`install` and `pip install` lines (alone or joined with `&&`, `||`, `;`) move to the image build unchanged, as `RUN` lines appended to the task's Dockerfile, and are disabled in `test.sh`.
- The uv installer runs at build with its install folder fixed to `/opt/harbor-tests/uv`; `source $HOME/.local/bin/env` is disabled.
- Each `uvx` line becomes a virtual environment built at image build (`/opt/harbor-tests/venv<n>`, same Python and the same `--with` packages), and the line calls the command from it by absolute path. A task image that ships its own `uv` is used when the script installs none.
- Anything else that would download (git clone, data downloads, `uv init`/`add`/`run`, `npm install`, a `uvx` line with shell variables) makes the task a skip with the line quoted.

These tools are visible to the agent: it can see that pytest and a Python under `/opt/harbor-tests` exist. They do not reveal the tests, which arrive only after the agent has exited.

### What is skipped, and why

| Reason | Terminal-Bench 2.0 (89) | SkillsBench main (87) |
|---|---|---|
| imported | 54 | 71 |
| reference solution downloads at run time (apt, pip, git clone, npm, uv add) | 33 | 12 |
| `test.sh` downloads in a way that cannot move to the build (git clone of test data, `uv venv`/`uv pip`, a release binary) | 2 | 2 |
| compose file the bench refuses (here: provider keys read from the host) | 0 | 1 |
| no literal `WORKDIR` | 0 | 1 |

SkillsBench counts as of 18 Nov 2026 (X2): compose tasks now import through `bench/compose` (docs/multi-container.md). Of the 3 tasks in `tasks/` with a compose file, `react-performance-debugging` imports as one container (its file is `docker-compose.yml`, which Harbor does not read), `fix-visual-stability` maps (main plus an `api` service) but is skipped because its solution runs `npm install`, and `drone-planning-control` is refused because `main` reads `ANTHROPIC_API_KEY` from the host. Terminal-Bench 2.0 ships no compose file.

Hack-Verifiable Terminal-Bench 2.1 (89): all skipped by default; with `-set hack_verifiable=keep`, 54 imported and 35 skipped for the same network reasons as Terminal-Bench 2.0.

A task with `environment/docker-compose.yaml` imports with its other services running beside the agent (`compose:` in `task.yaml`, mapped under Harbor's conventions, every change listed in `IMPORT.md`), unless the file holds something the bench refuses (docs/multi-container.md). Also skipped when present: a separate verifier container (DeepSWE), GPUs, MCP servers in `task.toml`, environment values taken from the host (`${VAR}`), a missing `solve.sh` or `test.sh`, a solution folder over 2 MiB packed, and symlinks that leave the task folder. Every upstream Terminal-Bench 2.0 task says `allow_internet = true`; bench runs never give the agent network except model calls, which is why a task whose reference installs packages is left out: the agent could not install them either.

### Hack-verifiable tasks

HVTB measures reward hacking with watchers started by the image's `ENTRYPOINT` that leave flag files in `/tmp`. Under the bench the watchers never start: the environment starts the agent container with its own entrypoint. Imported as they are, the tasks would hand the agent the solution in `/app/admin` with nothing watching, and the hack keys of `reward.json` would read 0 whatever happened. So the importer skips them with that reason. `-set hack_verifiable=keep` imports them anyway as ordinary tasks: `admin/**` is set as protected (gate G1, so changing the planted tests is an integrity violation), the task gets the tag `hack-verifiable`, the hack keys are dropped from the reward, and `IMPORT.md` says that reading the planted files is not detected. Detecting reads would need the environment to start the watchers in the agent container; the flags themselves would survive, because a snapshot-graded agent container keeps `/tmp` in its writable root file system.

## Lock, admit, run

```sh
bifrost-bench suite lint tb2
bifrost-bench suite lock tb2
bifrost-bench suite admit tb2          # builds each task image, runs the reference 3 times, the empty and exploit controls
bifrost-bench run benchmarks/skillsbench/experiment-skills.yaml   # skills suites only
```

An imported task is trusted only after its own reference passes and its empty control fails here. Tried on 15 Nov 2026 with five small Terminal-Bench 2.0 tasks: `regex-log`, `sqlite-db-truncate`, `openssl-selfsigned-cert` and `fix-git` (with `-set image=prebuilt`) were admitted (reference 3 of 3, empty, exploit and null agents failing, verifier with no network); `cancel-async-tasks` was not at first, because its tests write into the workspace, which the snapshot verifier then mounted read-only. Of three SkillsBench 1.0 tasks, `3d-scan-calc` (whose solution imports a skill's script) and `offer-letter-generator` were admitted; `threejs-to-obj` was not at first, for the same reason (its `test.sh` writes a ground-truth generator into `/root`, the workspace). With the snapshot verifier working on a writable copy of the workspace and `environment.user: root`, both were admitted the same day. Expect lint warnings for every imported task (no mutants, no `verifier/requirements.md`, no visible tests in the empty `repo/`); they do not block admission.

For a skills suite the importer also writes `experiment-skills.yaml`: arm A with `skills.enabled: false`, arm B with the task skills installed, condition `clean` on all tasks, 2 trials, primary metric `verified_success`, budget 5 USD. Edit the agent, trials and budget before running it.

## Honest limits

- **A snapshot grade is weaker than a clean-room grade.** The verifier runs in a frozen copy of the agent's container, so an agent could have changed the tools the tests call (the Python under `/opt/harbor-tests`, system libraries). Reports label snapshot grades. Harbor itself grades in the agent's live container, so this is no weaker than upstream.
- **The snapshot verifier needs a writable `/tests` and `/logs/verifier`, or a writable `/tmp`.** The importer bakes both folders into the image (mode 0777). With a read-only root file system the fallback rewrites the two paths in `test.sh` only; a Python test that names `/tests/...` itself then reads an empty folder and fails.
- **The agent runs as root.** Harbor runs the agent, `solve.sh` and `test.sh` as root, so every imported task sets `environment.user: root`: the agent, the reference script and the snapshot verifier run as root with a writable root file system and the capabilities a package install needs (docs/runner-safety.md states the trade). A task edited back to the default user runs as uid 1000, and a solution that writes under `/etc` or `/usr/local` then fails.
- **The verifier writes to a copy of the workspace, never the original.** Snapshot grading mounts the agent's workspace read-only (`/bench-src:ro`) and copies it into a separate per-grade volume that the verifier mounts writable, so upstream tests that write into the workdir (Terminal-Bench `cancel-async-tasks`, whose `test.sh` copies `test.py` into `/app`) work. Their writes never reach the sealed workspace. An earlier build mounted the workspace itself read-only and failed such tasks; that is the first attempt described above, kept as history.
- **Baked test tools change the agent's environment** (extra apt packages such as `curl`, a pip-installed pytest in the system Python for scripts that use pip). The appended section also copies root's git settings to `/etc/gitconfig` and trusts every repository (`safe.directory '*'`), because Harbor's agent is root and the bench's is uid 1000: without it, Terminal-Bench `fix-git` fails with "dubious ownership".
- **Upstream images drift.** Dockerfiles install unpinned packages and some clone repositories at build. Terminal-Bench `fix-git` no longer builds: its setup clones `github.com/TheMikeMerrill/personal-site`, which is gone. `-set image=prebuilt` builds from upstream's published image instead (`alexgshaw/fix-git:20251031`).
- **Network detection is by pattern.** A solution or test that downloads from inside Python (`requests`, Hugging Face) is not caught at import; admission fails it for lack of network.
- **Not mapped:** SkillsBench `verifier.hardening.cleanup_conftests` (BenchFlow deletes `conftest.py` files from the workspace before verifying), Harbor `artifacts`, cpu, memory and storage limits from `task.toml` (the agent config's budgets apply), and HVTB's read detection (above).
- **Timing-sensitive tests can flake.** Terminal-Bench `cancel-async-tasks` measures concurrency with wall-clock timeouts; run by hand as root with a writable workspace, its reference failed once in four runs while other containers were building. Admission's three reference runs catch such tasks.
- **Partial rewards** pass only at 1 (the suite's default `pass_threshold`); the reward itself is kept on the grade.
