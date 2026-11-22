# Importing ProgramBench

`bifrost-bench import programbench` turns ProgramBench into a suite. ProgramBench (Meta FAIR, arXiv 2605.03546, https://programbench.com) asks an agent to rebuild a whole program from scratch: the task image holds a compiled binary (`/workspace/executable`, execute-only) and the program's documentation; the agent writes a new codebase whose `./compile.sh` builds `./executable`, and hidden behavioural tests score it.

The code is `importers/programbench/`. It stages each upstream task as a Harbor-format folder and imports it with the harbor importer (`-set image=prebuilt`), so the suite follows `docs/importing-harbor.md`: one image per task, graded by a test script in a snapshot of the agent's container.

## Source (checked 21 Nov 2026)

| Part | Where | Version |
|---|---|---|
| Tasks (`task.yaml`, `tests.json`) | https://github.com/facebookresearch/ProgramBench, `src/programbench/data/tasks/` | commit `895f65916bc5ba34a915d36da321fde0301125ad` (12 Oct 2026) |
| Hidden test branches | https://huggingface.co/datasets/programbench/ProgramBench-Tests (not gated, 9.3 GB) | revision `5954a1957b5e0d770ec2c5523506abd212806315` |
| Images | Docker Hub `programbench/<owner>_1776_<repo>.<sha>:task_cleanroom_v6`, linux/amd64 only, about 0.9 GB compressed each (layers mostly shared) | tag `task_cleanroom_v6` (upstream's inference and eval tag) |

Licence: MIT (benchmark). Each program keeps its own licence; the test-blob folder carries `LICENSE` and `ATTRIBUTION.md` per task and both are copied into the task's verifier folder.

The task folder holds 201 entries; `testorg__calculator.abc1234` is upstream's own test fixture (no image, no tests) and is never imported. 200 tasks: 107 Rust, 46 Go, 33 C, 12 C++, 1 each Haskell, Java, Bash.

## Fetch and import

```sh
mkdir -p datasets/programbench && cd datasets/programbench
git clone https://github.com/facebookresearch/ProgramBench && git -C ProgramBench checkout 895f65916bc5ba34a915d36da321fde0301125ad
hf download programbench/ProgramBench-Tests --repo-type dataset --revision 5954a1957b5e0d770ec2c5523506abd212806315 --local-dir tests-hf
cd ../..
bifrost-bench import programbench -from datasets/programbench -suite programbench -set tests_revision=5954a1957b5e0d770ec2c5523506abd212806315
bifrost-bench suite lock programbench
```

Settings (`-set key=value`): `repo` and `tests` (default `<from>/ProgramBench`, `<from>/tests-hf`), `image_tag` (default `task_cleanroom_v6`), `agent_timeout` (default 7200 s; upstream's mini-swe-agent baseline allows 21600 s), `verifier_timeout` (default 3600 s), `revision`, `tests_revision`. `-ids` takes upstream folder names. Set `TMPDIR` on the same file system as the test blobs: staging hard-links them.

## How a task is converted

- `instruction.md`: upstream's mini-swe-agent ProgramBench prompt (task and rules: no source code, no wrapping the binary, no binary analysis), without the scaffold-specific tool-call text.
- Image: `FROM programbench/...:task_cleanroom_v6`, plus the pytest plugins the hidden tests' `eval/run.sh` installs at run time (`pytest-timeout 2.4.0`, `pytest-xdist 3.8.0`, `pytest-rerunfailures 16.4`), baked at image build because the verifier has no network.
- Verifier (`verifier/tests/upstream/test.sh`, from upstream `eval/eval.py`): delete workspace files whose sha256 is in `eval_clean_hashes`, remove `./executable`, seed git when absent, run `./compile.sh` (900 s, no network), stash the executable; for every non-ignored branch extract its archive into the workspace (removing the archive's top-level paths first), restore the executable, run `eval/run.sh` (1500 s, `--timeout-method=signal`, `--reruns=2`) and keep `eval/results.xml`. `grade.py` then computes upstream's score: passed / expected tests (`tests.json` minus ignored branches and ignored tests; a missing result counts as failed). The reward is that fraction; the task passes (resolved) only at 1. `/logs/verifier/pb/` keeps the compile log, per-branch logs and `score.json`.
- Reference control: ProgramBench ships no reference source in the agent's image, so the reference keeps a gzip of the original binary in `/opt/pb-reference` and writes a `compile.sh` that restores it. It proves the hidden tests pass with an executable that behaves like the original.

## Limits

- Snapshot grading, not upstream's clean room: upstream extracts only the submission archive into a fresh image. Here compile and tests run in a copy of the agent's container, so anything the agent left outside `/workspace` is visible to `compile.sh`. The agent runs as root (as the images do), so it can read the execute-only binary; copying, wrapping or compressing it is forbidden by the instruction (upstream detects it with an LLM judge) but not blocked by the verifier, exactly as the reference control shows. Review runs that score high.
- Per-branch isolation is approximate: upstream boots a fresh post-compile container per branch; here branches run one after another in the same workspace, with each archive's top-level paths cleared first.
- Verifier limits are the bench's snapshot limits (2 CPUs, 2 GiB memory). Large builds (ffmpeg, gdal, duckdb, php-src, gromacs) may not compile within 900 s or 2 GiB; such tasks fail admission and stay drafts.
- The reference control must score exactly 1. In the starter admission (fleet, 22 Nov 2026; 70 tasks checked) the original binary scored below 1 on 40 tasks (from 0.36 to 0.9997: tests that depend on timing, terminal size, network or the host), so those tasks stay unadmitted and 30 were admitted. A task whose original binary cannot pass every hidden test here cannot be resolved by any agent, so lowering the threshold was not done.
