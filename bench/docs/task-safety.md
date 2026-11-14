# Task safety: what admission checks before anything runs

A task is a folder somebody wrote by hand or cut from a repository: a repo
snapshot, an instruction, hidden verifier tests, controls and fixtures. The
bench copies parts of it into agent workspaces, bind-mounts parts of it into
grading containers and builds the suite's base image from a Dockerfile next
to it. This page says what the bench now checks about such a folder, the
rules for paths and symlinks, and what is still left to people.

The code is `utils/paths.go` (path resolution), `suite/loader.go` (load-time
checks), `suite/lint.go` and `suite/validate.go` (static validation),
`suite/admission.go` and `cmd/bifrost-bench/suite.go` (admission order), and
the run-time checks in `environments/dockerworkspace.go`,
`graders/cleanroom.go`, `probes/fixture.go` and `scaffold/taskinit.go`.

## What admission guarantees

`task admit <suite>/<task>` and `suite admit <suite>` run in this order:

1. **Load.** The suite and every task load with the path rules below. A task
   that breaks them does not load, so it cannot be validated, admitted,
   planned or shown as runnable.
2. **Static validation.** The same checks `task validate` runs, for every
   task being admitted, plus the suite-level lint (`suite lint`): schema,
   duplicate ids, probe layers, the EDIT ME marker in the instruction or a
   verifier file, words in the instruction that reveal grading, hidden test
   files that are also visible in `repo/`, required test ids that name no
   hidden test (Python and Go), stale notes missing while the suite runs the
   stale-notes probe, a `.git` folder in `repo/`, the task missing from
   `suite.yaml`, the secret scan, symlinks, sizes, and the reference diff and
   every mutant applying cleanly to a copy of `repo/`.
3. **Refusal.** Any error from step 2 stops admission with the same lines
   `task validate` prints. Warnings are printed and do not block. Nothing has
   been built or started at that point: no `docker build`, no container, and
   `suite.lock` is not written.
4. **Images and controls.** Only then are missing images built and the
   Docker controls run (reference three times, empty patch, mutants,
   exploit, stale notes, null agents).

`suite.Admit`, the library call behind both commands, runs the same static
validation itself and records it as the first check in the lock, named
`static_validation` (want `clean`). A task with errors gets a record holding
only that failed check, and no workspace is created for it.

Required test ids: for JUnit suites (`hidden_tests`, and `verifier_command`
with `result: junit`), an id in `verifier.required` that never appears in the
verifier's report makes the grade fail with `required tests did not run:
<id>`. At admission the reference runs then fail and the check detail names
the id (`missing_required=<id>`), so the task is not admitted. Python and Go
ids are also checked statically; Node ids (`test::<name>`) can only be
checked by that admission run. A suite whose verifier reports `reward` or
`exit_code` ignores `required`, and lint warns about it.

Sizes: `task validate` warns about every file over 5 MB in the task folder
and about a `repo/` with more than 5,000 files, and refuses a task folder
over 200 MB. Every run copies the repo, so large tasks slow every run.

## Path rules

Every path a task or suite names (`instruction`, `repo_dir`, `notes.clean`,
`notes.stale`, `verifier.tests`, every control, `base_image.dockerfile`,
probe overlay sources) must:

- be relative, with no `..` that leaves its folder;
- exist;
- still lie inside its folder after every symlink on the way is resolved.
  The folder is the task folder for task paths and the suite folder for
  suite paths. The task folder itself must resolve inside the suite folder.

A lexical check is not enough because `os.Open`, Docker bind mounts and the
`docker build` context all follow symlinks: `repo_dir: repo` where `repo` is
a link to `/etc`, or a notes file that links to `~/.ssh/id_rsa`, would hand a
host file to the agent or the model.

Symlinks inside the task folder (in `repo/`, `verifier/`, `solution/`,
`controls/`, `fixtures/` or anywhere else in it):

| The link | Result |
|---|---|
| resolves inside the task folder | allowed; `suite lint` and `task validate` list the links in one warning |
| resolves outside the task folder, also through a chain of links that each look inside (`a/l -> ..`, `a/m -> l/..`) | the task does not load |
| has an absolute target | does not load, even when the target is inside today: it means something else on another machine and in a container |
| dangles | judged by where it would point |
| loops | does not load |

Links that stay inside are copied as links into the workspace and the
grading volume (`cp -d`, `cp -a`), never as the file they point to. The same
rule covers `probes/`, `fixtures/` and the base Dockerfile's folder of a
suite, against the suite folder.

`task init` never follows links in your repository: it reads commits with git
plumbing. It refuses a link whose target is absolute or leaves the
repository, and after writing the task it checks the written `repo/` and
`verifier/tests/` again with every link resolved, which catches chains.

### At run time

A path that was fine at load time could be swapped for a link afterwards.
The places that hand task files to Docker or read them for the agent resolve
again right before they do and use the real path:

- the workspace: `repo_dir` and `notes.clean` before they are bind-mounted
  into the trusted helper that fills the volume;
- grading: `repo_dir` and `verifier.tests` before the clean room mounts them;
- probe fixtures: read through an `os.Root` of the task or suite folder, so
  a link out of that folder is refused even if it appears after the check;
- the suite's base Dockerfile, before `docker build`.

## What this does not cover

- **Hidden tests cut from a reference commit.** Admission proves the
  reference passes and the controls fail. It cannot prove the tests check
  what the instruction asks, or that they accept a correct fix written
  differently. A person still has to read them; `task validate` reminds you.
- **Docker is not a hard boundary.** Agent and verifier containers share the
  host kernel. They run without capabilities, as uid 1000, with a read-only
  root, no Docker socket and no host bind mounts, which closes the common
  escapes, not a kernel exploit. Run untrusted agents on a host you can lose.
- **No disk quota.** Workspace and grading volumes have no size limit; an
  agent can fill the Docker disk. The 200 MB task limit bounds the input,
  not what a run writes.
- **A narrow race remains for bind mounts.** Docker cannot mount an open
  file descriptor, so between the bench resolving a path and the Docker
  daemon mounting it, someone who can write the task folder on the host
  could swap a folder for a link. That needs write access to the host's task
  folder during the run; agents never get it.
- **Hard links are not detected.** A hard link to a host file looks like a
  regular file. Git does not store hard links, so tasks cut by `task init` or
  checked out from git have none.
- **Agent and experiment files are the operator's.** An agent's
  `notes.user_file` is an absolute host path by design, and the files an
  experiment's arms point at (`experiments/fixtures/...`) are resolved by the
  planner, not checked by admission. Keep them in your own project folder.
  An agent's `files` are content templates, not host paths.
- **Run-time readers outside the paths above** (the adapters reading the
  instruction, gates reading repo files) rely on the load-time check; they do
  not resolve again.
