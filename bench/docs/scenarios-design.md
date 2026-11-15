# Scenarios: design contract

Status: implemented (15 Nov 2026) and checked with scripted controls through
the full pipeline; the user guide is `docs/scenarios.md` and the state of each
importer is in the README. This file fixes the names, shapes and ownership
every part agrees on. The types are in `schemas/scenario.go`; where
this file and the code differ, the code wins and this file is corrected.

## Goal

The bench could run one shape of task: edit a repository, pass hidden tests.
Companies also need to test agents that call tools, use MCP servers, remember
across sessions, use skills, and answer questions, and they want the public
benchmarks for each. One generalization covers all of them:

**scenario = workspace (optional) + services + sessions + output + verifier**

Everything that already exists keeps working unchanged: planning, the frozen
manifest, slots, fault conditions, gates, the sign-flip verdict, the
leaderboard, the release gate and sealed evidence. A scenario only changes
what a run is made of and what its verifier reads.

## Rules that do not bend

1. **Old hashes stay.** Every new field is optional and omitted when empty.
   A suite, manifest, lock or seal written before scenarios must hash to the
   same value. There is a test for this on `testdata/recorded`.
2. **The verifier is hidden and runs apart.** Hidden files (tests, expected
   answers, expected state) reach a container only after the agent process is
   gone. The agent never sees `/grader`.
3. **Unknown is never zero.** A tool ledger that is missing, a service that
   did not come up, an answer that was not written: each is reported as what
   it is, with the reason.
4. **Infrastructure failure is not agent failure.** A service that fails its
   ready check, a tool proxy that dies, a task image that does not build: the
   run is `aborted_infra` (before the agent started) or `infra_error`.
5. **No judge in a verified suite.** A judge model decides outcomes only in a
   suite that declares `grading: judged`. Reports, the leaderboard and the
   verdict header label it, and a judged suite never shares a comparison
   with a verified one.
6. **Imported data is not committed.** Importers read a local copy of the
   upstream benchmark and write a suite whose folder is git-ignored. The
   repository ships importers, small hand-written fixtures in upstream
   format, and docs.
7. **Nothing reaches the network at run time** except model calls through the
   gateway and declared external API routes through the tool proxy.
   Services, the agent and verifiers have no route out. The tool proxy has
   one only for an external API (`Task.External`, `docs/external-apis.md`)
   in an experiment that opts in to `external: live` or `record`, and then
   only to that API's host and port through a per-run forwarder; those runs
   are labelled not repeatable. In replay mode, the default, the proxy has
   no route out either. Image builds may use the network when a task says
   `build_network: true`.

## The seven pieces

### 1. Per-task image (`Task.Environment`)

A task may bring its own image or Dockerfile. The image is built at admission
and at plan time and tagged `bench/task-<suite>-<task>:<12 hex of the content
hash>`; its image id is recorded in the manifest. `workspace_path` names the
folder inside the image that seeds the workspace volume; the workspace is
still a volume with a git baseline, so the diff, G1 and the allowlist work.

The agent must run inside that image. An agent whose build declares
`build.base_arg` is built once per task image and tagged
`<tag>-<suite>-<task>`. An agent without `base_arg` runs in its own image and
sees only the workspace files; `doctor` and `agent check` warn.

`environment.user: root` (the only accepted value) runs the agent, a scripted
control and the verifier as root inside the task's own image; it is ignored
for a task without one. Imported Harbor tasks set it, because upstream tasks
install packages and change files under `/etc`. The trade is plain: a root
agent can change anything in its container, its root file system is
writable, and it gets back the capabilities a package install needs
(`utils.RootCaps`: chown, dac_override, fowner, fsetid, kill, setuid,
setgid, setfcap, net_bind_service, sys_chroot, audit_write). It still has no
privileged mode, no host mounts, no docker socket, only the internal
networks, and no-new-privileges; a container escape would start from root
instead of uid 1000. The plan records the user (`plan.json`).

### 2. Verifier modes (`SuiteVerifier.Mode`)

`clean_room` (default): pristine workspace plus the agent's diff, in the
suite's base image or the task's `verifier_image`.

`snapshot`: after the agent exits, its container file system is frozen
(`docker commit`), and the verifier runs in a fresh container from that
image, with no network, the workspace volume mounted read-only at the workdir
and the hidden files at `/grader`. It is for tasks whose result is outside
the workspace. `Grade.Mode` is `snapshot`; reports label it as weaker than a
clean-room grade because the agent could have changed the verifier's tools.
G1 and the regrade gate still apply to the workspace.

### 3. Services (`Suite.Services`, `Task.Services`)

Containers started beside the agent in the prepare stage, after the
workspace, suite services first, then task services, in file order. A run
gets its own internal network so two runs never see each other's services;
the agent is on that network and on the evaluation network. Each service must
pass its ready check or the run is `aborted_infra`. After the agent exits,
every `export` is read into `state/<service>/<export>` in the run folder,
sealed, and mounted read-only in the verifier at `/grader/state/...`. The
existing `verifier_command` grader then decides, in any of its three result
formats: a state verifier is an ordinary verifier that reads `/grader/state`.

Track `browser` (`schemas.TrackBrowser`) is built from these pieces only: a
proxied `mcp` service running Playwright MCP with a headless Chromium, and
the web application as a `hidden` service that only the browser reaches,
graded on the web application's export (`docs/browser-tasks.md`).

### 4. Tool proxy (`bench/toolproxy`, command `bifrost-bench toolproxy`)

One per run, started as a service by the engine when any service is proxied.
The agent reaches a proxied service at `http://toolproxy:9200/s/<service id>/...`
and the proxy forwards to the service. It understands MCP streamable HTTP
(JSON-RPC `tools/call`, `tools/list`) and plain HTTP. Every call is appended
to `tools.jsonl` (`schemas.ToolCall`) in a folder the engine reads at collect.
A probe of layer `tool` (`ProbeApply.Tool`, `schemas.ToolFault`) makes the
proxy answer with an error, time out, or append hostile text to a real
result, with a per-run nonce in the ledger as the receipt.

Evidence source `tool_ledger` fills `Evidence.ToolCalls`. Metrics:
`tool_calls_proxied`, `tool_error_rate`, `tool_recovery` (a failed call
followed by a later success on the same server), `distinct_tools`. All are
unknown, not zero, when the task has proxied services and the ledger is
missing.

### 5. Sessions (`Task.Sessions`)

A run of a task with sessions executes the agent once per session, in order.
Between sessions the workspace is reset to the task's starting state (unless
`keep_workspace`), the session's `files` are copied in, and the instruction
is that session's. What persists: the `/memory` volume (path in
`BENCH_MEMORY_DIR`) and the agent's `memory.paths`. The agent also gets
`BENCH_SESSION_ID`, `BENCH_SESSION_INDEX` and `BENCH_SESSION_COUNT`. The
gateway session id stays the run's, so cost and tokens cover every session.
Any session that times out or crashes ends the run with that outcome. Only
the last session's output is graded.

### 6. Outputs and graders

| `output.kind` | What is exported | Graders |
|---|---|---|
| `diff` (default) | `workspace.diff` | `hidden_tests`, `verifier_command` |
| `state` | `state/<service>/<export>` (and the diff) | `verifier_command` |
| `answer` | `answer.txt` (from `answer_path`, else the final message; source recorded) | `answer_match`, `judge`, `verifier_command` (answer mounted at `/grader/answer.txt`) |
| `artifact` | `artifacts/<id>/<name>`: the files `output.artifacts` names (a file or a glob, relative to the workspace), copied by a trusted helper (and the diff) | `verifier_command` (mounted read-only at `/grader/artifacts/<id>/<name>`) |

An artifact task's files are copied after the agent exits by a trusted helper
with no network and the workspace read-only. A match that resolves outside
the workspace through a symlink, is not a regular file, has a name outside
`[A-Za-z0-9_][A-Za-z0-9._-]*`, or is over the per-file cap (`max_bytes`,
default 32 MiB) or the run's total cap (128 MiB) is recorded in
`Evidence.Artifacts` with its error and not copied; a glob copies at most 64
files. The outcome follows the answer kind: output the agent did not
produce is the agent's failure, so a `required` artifact without one usable
file fails the run with the reason `artifact output: required artifact
missing: ...` and the verifier is not run; an export that broke on the
bench's side (the helper could not run, its output was malformed) is
`invalid_evaluator` with the reason `artifact output: export failed: ...`,
because the verifier's input is the bench's to deliver. Artifact files are
sealed with the run folder, `regate` checks their sha256 against the
evidence, and the API serves them only as downloads
(`application/octet-stream`, `nosniff`, attachment), never rendered.
Admission controls of an artifact task are scripts (`.sh`) run in the
agent's place that write the files.

`answer_match` checks the hidden `AnswerSpec` (exact, contains_all, regex,
numeric, choice, json) with no model. `judge` asks the suite's pinned
`JudgeSpec` model through the gateway on a separate key, with temperature 0
and an odd number of votes; `Grade.Judged` is true and `Grade.Judge` records
the model, prompt hash, each vote and the judge's cost, which is never added
to the agent's cost. An unparsable judge answer is `invalid_evaluator`.

Admission controls follow the output kind (`schemas.TaskControls`): the
reference is a patch, a script run in the agent's place, or an answer file.
For a judged suite the reference answer must pass three times in a row and
every `wrong_answers` entry must fail: that is the judge's calibration, and
it is recorded in `suite.lock` like any admission.

### 7. Skills, MCP and memory on the agent (`AgentConfig`)

- `skills`: skill folders (each holding `SKILL.md`) from the task
  (`Task.Skills`) and from the config (`skills.extra`) are installed in the
  adapter's native skills folder, or `skills.dir`. Their content hash is in
  the manifest. `skills.enabled: false` installs none; an experiment with one
  arm setting that is a skill comparison, decided by the usual paired test.
- `mcp`: each proxied `mcp` service becomes an entry in the agent's own
  config format (Codex TOML, Claude Code JSON, OpenCode JSON, or generic JSON
  and `BENCH_MCP_SERVERS` for `command` agents).
- `memory.paths`: kept between sessions.
- `remote`: adapter `remote_http`, for agents that are deployed services.
  Only `answer` tasks. With `model_traffic: external` tokens, cost and steps
  are unknown.

### 8. Importers (`bench/importers`, command `bifrost-bench import`)

`bifrost-bench import list` and `bifrost-bench import <format> -from <path>
-suite <id> [-ids] [-limit] [-set k=v]`. An importer writes an ordinary
suite with `imported:` set, `provenance.imported_from` on each task, and a
`.gitignore`. After import the user runs `suite lock` and `suite admit`: an
imported task is trusted only after its own reference passes and its empty
control fails here.

| Format | Benchmarks | Track | Verifier |
|---|---|---|---|
| `harbor` | Terminal-Bench 2.x, Hack-Verifiable TB, SkillsBench | terminal, skills | snapshot, reward |
| `swebench` | SWE-rebench, SWE-bench-Live, DeepSWE | coding | clean_room or snapshot, tests |
| `impossiblebench` | ImpossibleBench | safety | `impossible: true`, gate `impossible_pass` |
| `mcpmark` | MCPMark (local servers) | mcp | state |
| `tau2` | tau2-bench domains | tool_use | state |
| `agentdojo` | AgentDojo suites | safety | state plus gate `tool_injection_followed` |
| `longmemeval`, `locomo` | LongMemEval, LoCoMo | memory | judged (`answer_match` where exact) |

### 9. Remote workers

`bifrost-bench worker -server <url>` runs on another machine with the same
project at the same commit and its own stack. It registers, leases slots
over HTTP, runs them with the normal pipeline, and uploads each sealed run
folder; the coordinator verifies the seal before accepting it. The worker API
is off unless `BENCH_WORKER_TOKEN` is set, and every call carries it. The
coordinator owns the spend cap and the lease table, so the cap holds across
workers. A worker whose manifest hash, suite lock hash or part versions
differ is refused.

## Ownership

Each area has one owner. A file not listed belongs to whoever created it.
Outside your area you may add one clearly marked call line or one registry
entry, after re-reading the file; never restructure another owner's file.
`schemas/*` belongs to the lead: adding an optional field is allowed, renaming
or removing is not.

| Area | Owns |
|---|---|
| Environment kernel | `environments/`, `graders/cleanroom.go`, `graders/verifiercommand.go`, `graders/hiddentests.go`, `engine/image.go`, `engine/janitor.go`, `utils/docker.go` |
| Run kernel | `engine/pipeline.go`, `engine/stages.go`, `engine/planner.go`, `engine/checkplan.go`, `engine/regate.go`, `suite/`, `store/`, `evidence/`, `adapters/scripted.go`, run detail in `handlers/` |
| Harbor importer | `importers/harbor/` |
| SWE importer and impossible tasks | `importers/swebench/`, `importers/impossiblebench/`, `gates/impossiblepass.go` |
| Agent wiring | `adapters/` except `scripted.go` and `remotehttp.go`, skills and MCP config rendering, skill metrics |
| Tool proxy and MCP | `toolproxy/`, `cmd/bifrost-bench/toolproxy.go`, tool evidence source, tool metrics, tool probe layer, `importers/mcpmark/` |
| Tool use | `importers/tau2/`, tau2 service images |
| Memory and answers | `graders/answermatch.go`, `graders/judge.go`, `importers/longmemeval/`, `importers/locomo/`, memory metrics |
| Tool safety | `importers/agentdojo/`, `gates/toolinjection.go`, tool-poison probes |
| Remote | `engine/remote*.go`, `engine/scheduler.go`, `handlers/worker*.go`, `cmd/bifrost-bench/worker.go`, `adapters/remotehttp.go` |
| Company side | `scaffold/`, `examples/`, `cmd/bifrost-bench/suite.go`, `task.go`, `agentcheck.go`, `agentrows.go`, `init.go`, user guides in `docs/`, `README.md` |
| UI | `ui/`, `handlers/imports.go` |
