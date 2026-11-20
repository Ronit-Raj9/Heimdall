# Lock fingerprints: binding every evaluator setting

`suite.lock` pins a suite version. Its original fields hash selected files
per task (instruction, repo, verifier tests, controls, fixtures, plus the
scenario inputs folded into them) and each probe file. They did not bind the
rest of what decides a grade: the verifier command, result format, pass
threshold, restore list, timeout and mode in `suite.yaml`, the required test
ids, protected paths and timeouts in `task.yaml`, which tasks and probes the
suite lists, the grader and environment versions, or the images. Any of
those could change and new runs would still go out under the old admission
record.

The fingerprints section closes that gap. It is additive and versioned: a
lock written before it existed has no such section, is a "legacy lock", and
encodes byte for byte as it always did, so every manifest, seal and recorded
run that points at it keeps verifying.

The code is `suite/fingerprint.go` (computing and comparing),
`suite/lock.go` (`LockWith`, admission carry-over, `Diff`),
`engine/fingerprints.go` (plan and run enforcement), `engine/suiteimages.go`
and `environments/image.go` (image ids), and
`cmd/bifrost-bench/lockfingerprints.go` (CLI and doctor).

## What is fingerprinted

```json
"fingerprints": {
  "version": 1,
  "suite":  {"settings": "<sha256>", "fields": {"verifier": "<sha256>", ...}, "files": "<sha256>", "images": {"base": "tag only: bench/coding-core-base:1"}},
  "tasks":  {"t01": {"settings": "...", "fields": {...}, "files": "...", "images": {...}}},
  "probes": {"stale-notes": {"settings": "...", "fields": {...}, "files": "..."}}
}
```

Each fingerprint has three parts.

- **settings**: the sha256 of the canonical JSON of the parsed YAML, and in
  `fields` the sha256 of each top-level key, so drift names the key that
  changed.
  - Suite: every key of `suite.yaml` except `description` and `imported`
    (metadata): `id`, `version`, `environment`, `grader`, `base_image`,
    `workdir`, `verifier` (command, restore, timeout_s, result, result_path,
    pass_threshold, mode), `tasks` and `probes` in order, `track`,
    `grading`, `output`, `services`, `judge`, `external` and `canary`; plus
    `parts`, the versions of the suite's grader and environment parts as the
    registry reports them (`grader/<name>`, `environment/<name>`).
  - Task: the whole parsed `task.yaml`, every field, including `split`,
    `verifier.required`, `protected`, `timeouts`, `environment`, `services`,
    `sessions`, `external` and `controls`.
  - Probe: the whole parsed probe YAML.
- **files**: the sha256 over every file and folder those settings name, in
  the `utils.TreeHash` line format (relative path, NUL, file sha256,
  newline, sorted), with volatile junk left out.
  - Task: the whole task folder. Every path a task names must resolve inside
    it (`docs/task-safety.md`), so instruction, repo, verifier with answer
    and original tests, controls, fixtures, notes, session files, skills,
    service build contexts, cassettes and seed files are all covered and
    none can be missed.
  - Suite: the base image build context (the folder of
    `base_image.dockerfile`), `fixtures/` and `services/` at the suite root,
    every file and build context the suite's services name, and the
    cassettes of the suite's external APIs. A build context that is the
    suite root itself leaves out `suite.lock`, `suite.health.json`,
    `tasks/`, `probes/`, `runs/` and `checks/`.
  - Probe: the probe YAML file and every suite fixture an overlay reads
    through `{{suite.root}}`. A source with a template variable past the
    suite root (`{{suite.root}}/fixtures/safety/{{task.project}}/x`) binds
    the folder before the variable, a superset of what it can render to.
    Overlays without `{{suite.root}}` read task fixtures, which the task
    fingerprint binds.
- **images**: image role to identity, in one of three forms:
  - `tag only: <tag>`: the default for a locally built image. A local build
    is not reproducible: the same Dockerfile built on another machine (`make
    images`, a fresh clone, CI) gets another image id, so a lock that pinned
    the id would refuse every machine but the one that wrote it. The build
    context (the Dockerfile and every file beside it) IS hashed in the files
    part, so a changed Dockerfile or build file is still caught as drift.
  - `<tag>=<repo>@sha256:<hex>`: a registry digest, recorded when docker
    reports one for the image that differs from its local id (an image
    pulled from a registry on Docker's classic image store). It is the same
    on every machine that pulls it and is compared by digest. Docker's
    containerd image store reports a digest equal to the local id for every
    image, local builds included, so there every image is recorded by tag
    only.
  - `<tag>@sha256:<hex>`: a local image id, only with `suite lock
    -pin-local-images`. Strict and valid only on the machine (or the
    registry) that built it; use it for a team's own machine. Once a lock
    pins ids, later `suite lock` and `suite admit` keep pinning until `suite
    lock -pin-local-images=false`.

  Suite roles: `base` and `helper` (the `images.helper` of the bench
  config). Task role: `task` (a task's own image). Tool proxy and service
  images are not in the lock, because `suite admit` and routine rebuilds
  replace them; their source is in the files hash, and each experiment
  freezes their ids in its manifest (below).

Canonical JSON drops empty values (null, empty string, false, 0, empty list,
empty object) before hashing, so a field added to a schema later and left
empty never changes a fingerprint, while any value that is set does.

Junk excluded from every files hash: `__pycache__` folders, `*.pyc`,
`*.pyo`, `.DS_Store`, `Thumbs.db`, and editor swap and backup files
(`*.swp`, `*.swo`, `*~`, `.#*`, `#*#`). Only the fingerprints skip them; the
legacy lock fields hash every file as before.

## Enforcement

| Where | Lock with fingerprints, drift found | Legacy lock |
|---|---|---|
| `plan` (and a remote worker's replan) | refused: `suite lock fingerprints drifted, refusing to plan: suite coding-core: settings changed (verifier); task t01: files changed; ...` | plans as before, with one warning line: "legacy lock: evaluator settings are not bound; run suite lock to add fingerprints" |
| `run` and `run -resume` (scheduler start) | refused the same way, `refusing to run` | as before, same warning |
| a smoke experiment (`smoke: true`) or `agent check` | warning only | as before |
| `suite lock` | prints the drift as a diff (`fingerprint.settings.verifier`, `fingerprint.files`, `fingerprint.image.base`, MAJOR rerun) and writes the new lock; a task whose suite or task fingerprint changed goes back to draft | first lock prints `fingerprints added` (PATCH) and keeps every admission whose content hashes are unchanged |
| `suite admit`, `task admit` | each admission record stores `fingerprint`: the sha256 of the suite and task settings and files it checked | not stamped |
| `doctor` | `suite <id> lock fingerprints` row: fail on drift | warn row with the legacy line |
| suite page and `GET /api/bench/suites` | `lock_kind: fingerprinted`, `fingerprint_drift` rows, state `stale` | `lock_kind: legacy` |

A task's admission holds only for the fingerprints it was recorded under:
the planner refuses a task whose admission record carries a fingerprint
other than the lock's (`task t01 admission was recorded for other lock
fingerprints, so it is no longer admitted; run suite admit`). Records written
before fingerprints existed carry none and are taken as they are, so adding
fingerprints to an admitted pack keeps it admitted.

`tasks mine` and `task review` re-record the admissions of the previous lock
when they write a new one. A record is carried only when its task
fingerprint and the previous lock's suite fingerprint (every settings field
except `tasks`, the suite files and the image identities) equal the new
lock's; adding task ids is the one suite change allowed. Any other suite
change, even one made before the command started, drops the record and the
task needs `task admit` again.

Images: a tag-only identity needs the same tag and nothing else, so a lock
in this repository plans on any machine that built the same Dockerfiles. A
recorded registry digest that the image no longer has, or a pinned local id
that the tag no longer names, is drift (refused). A digest or pinned id whose
image cannot be inspected now (CI, a fresh clone, no docker) is not checked
and prints one warning, never a refusal. A role the current environment does
not report (no helper configured) is also only a warning. Re-locking on a
machine without images keeps a recorded digest (and, while pinning, a
recorded id) when the tag is unchanged. An identity changing from a local id
to tag only of the same tag is no content change: admissions and
`created_at` are kept and `suite lock` reports nothing.

Each experiment still freezes the local image ids it planned with
(`suite_images`, `arms.*.image_id`, `task_images`, below): that protection is
machine local by nature and right for the life of one experiment.

## Manifests and image ids

A plan records, additively and only when present:

- `fingerprint_hash`: the sha256 of the canonical JSON of the lock's
  fingerprints section, so a run traces to the exact evaluator settings it
  ran under. Empty for a legacy lock.
- `suite_images`: role to local image id for `base`, `helper`, `toolproxy`
  (when used), `service/<id>` for a suite service and
  `task/<task id>/service/<id>` for a task's own service (compose services
  included; checked only by that task's runs), beside the existing `arms.*.image_id`,
  `arms.*.task_images` and `task_images`. A tag that cannot be inspected is
  left out with a warning.

Before each run the environment compares every recorded id with what the
tag names now and refuses a mismatch (`base image bench/coding-core-base:1
is now ... but experiment ... was planned with ...; restore that image or
plan a new experiment`). A missing image or a role the run does not name is
not checked and warns. A manifest without `suite_images` is not checked, so
every existing manifest runs and hashes as before.

## Old experiments stay valid and comparable

- The legacy fields are computed and encoded exactly as before. Adding
  fingerprints keeps `created_at` when nothing else changed, so the lock
  with its fingerprints section removed re-encodes to the previous bytes:
  `suite.LegacyLockHash` of the new lock equals the old lock hash. Resume
  (`run -resume`) and `regate` accept a manifest whose `suite_lock_hash` is
  either the lock hash or that legacy hash, so an experiment planned under
  the legacy lock still resumes and regates after `suite lock` adds the
  section.
- Nothing in analysis keys on the suite lock hash. The leaderboard,
  verdicts, trends and agent diff group by `suite@version`, config hash and
  task identity (computed from the legacy task hashes, unchanged), so old
  and new runs of the same unchanged content stay on one board. `check` on a
  sealed experiment reads the manifest and the sealed runs, never the live
  lock.
- Older binaries refuse a lock with a fingerprints section (the lock
  decoder rejects unknown fields). Use the binary that wrote it.

## Workflow

```sh
bifrost-bench suite lock coding-core      # adds or refreshes fingerprints, prints drift
bifrost-bench doctor                      # one "lock fingerprints" row per suite
# change a verifier command, a required test id, a timeout, a probe list ...
bifrost-bench plan -exp experiments/x.yaml   # refused, names what drifted
bifrost-bench suite lock coding-core      # records the change; changed tasks go back to draft
bifrost-bench suite admit coding-core     # re-admits under the new fingerprints
```

## Limits

- Fingerprints bind what the suite folder and the part versions say. A
  change inside a grader or environment implementation that does not bump
  its part version is not seen; the part version is the contract.
- Locally built images are bound by tag only unless pinned, because local
  builds are not reproducible; what they are built from is bound through the
  files hash of their build context. A base image pulled by tag (`FROM
  python:3.12-slim`) can move under the same Dockerfile; pin it by digest in
  the Dockerfile to bind it. Registry digests are read from the local
  docker, never resolved against a registry.
- The helper and tool proxy tags come from the bench config of the machine
  that locks; another machine with different tags warns or drifts until it
  re-locks.
