# Importing CooperBench

`bifrost-bench import cooperbench` turns the feature pairs of
[CooperBench](https://arxiv.org/abs/2601.13295) ("CooperBench: Why Coding
Agents Cannot be Your Teammates Yet", Stanford and SAP Labs,
[cooperbench.com](https://cooperbench.com)) into two-member team tasks
(docs/multi-agent.md): each member implements one feature of the same
repository, the team mailbox is the messaging channel, and upstream's own
runner and per-feature tests decide.

## What upstream is

Checked on 22 Nov 2026 against commit
`63b9d44d9f39a02fccf5bf0052db48a917a011fd` of `cooperbench/CooperBench`
(main, 14 Sep 2026).

- **Licence.** MIT (README badge, `pyproject.toml` `license = {text = "MIT"}`;
  the repository has no `LICENSE` file). Every suite gets `LICENSE.cooperbench`
  and `NOTICE.md`.
- **Data.** `dataset/<repo>_task/task<id>/`: `Dockerfile`, `runner.sh`
  (the image's entrypoint), `combined.patch` (the whole PR, every feature,
  conflicts resolved) and `feature<N>/{feature.md, feature.patch, tests.patch}`.
  30 tasks, 199 features, 12 repositories (Python, Go, TypeScript, Rust).
  `dataset/subsets/all.json` lists all 652 pairs; `lite`, `flash`, `core`,
  `flash_10`, `team3_*` are smaller lists.
- **Images.** `akhatua/cooperbench-<repo>:task<id>` on Docker Hub
  (multi-arch), repository at the base commit in `/workspace/repo`, its
  dependencies installed.
- **Coop setting.** Two agents, one feature each, each in its own container;
  they message through Redis (`coop-send`, `coop-recv`, `coop-broadcast`).
  Each agent's patch is collected, the two are merged with git, and the
  merged tree is run through `runner.sh` once per feature with that feature's
  `tests.patch`. A feature passes when the runner exits 0 and the parsed
  output shows at least one passed test; the pair passes when both do
  (`both_passed`). Test files in agent patches are dropped first.

## Importing

```
git clone https://github.com/cooperbench/CooperBench.git datasets/cooperbench
bifrost-bench import cooperbench -from datasets/cooperbench -suite cooperbench            # all 652 pairs
bifrost-bench import cooperbench -from datasets/cooperbench -suite cb-lite -set subset=lite
bifrost-bench import cooperbench -from datasets/cooperbench -suite cooperbench -ids go_chi_task/task26   # every pair of one task
```

`-ids` takes bench ids (`go-chi-task26-f1-f2`), upstream ids
(`go_chi_task/task26/features_1_2`) or a whole task (`go_chi_task/task26`).
Settings: `subset` (default `all`), `revision`, `version`, `agent_s`
(default 1800).

## The mapping

| Upstream | Here |
|---|---|
| a pair (task, feature i, feature j) | one task `<repo>-task<id>-f<i>-f<j>`, `team: agent1 (lead), agent2 (worker)` |
| agent1 / agent2 feature spec | instruction sections Feature A (agent1) and Feature B (agent2) |
| Redis `coop-send` / `coop-recv` | the team mailbox (`HEIMDALL_MAILBOX_URL`, `/send`, `/recv`), every message a ledger row |
| the task image | `environment.image`, `workspace_path: /workspace/repo`, `user: root` |
| merge of the two patches, then `runner.sh` per feature | the diff of the shared tree against the base commit (test files dropped), then a trusted copy of `runner.sh` per feature (`verify.py`, snapshot mode) |
| `both_passed` | reward 1, pass threshold 1; dimensions `feature1_passed`, `feature2_passed`, test counts, `patch_bytes` |

Controls: the reference applies `combined.patch` (alone at admission; in a
team oracle run `agent2` skips it, any other member or a lone run applies it), the empty control changes
nothing, and the mutant applies only the first feature's gold patch, so the
second feature's tests fail.

## What differs from upstream

- **One shared tree.** Upstream gives each agent its own container and copy
  and merges two patches; a git conflict fails the pair. Here both members
  edit the same working tree (the bench shares the workspace volume across a
  team), so conflicts show as edits that overwrite each other, not as a
  failed merge. Scores are not comparable with the paper's.
- **Both specs visible.** The instruction holds both features; upstream
  shows each agent only its own.
- **No network at grading.** The runner's install lines (`pip install`,
  `uv sync`, `pnpm install`, ...) get `|| true` and pip, cargo, go and uv run
  offline; the images already hold the dependencies.
- **Trusted runner.** The runner comes from the task folder, not from the
  image's `/usr/local/bin/runner.sh`, which a root agent could change.
