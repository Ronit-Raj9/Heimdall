# Importing ToolBench-X

`bifrost-bench import toolbench-x` turns
[ToolBench-X](https://huggingface.co/datasets/Foreverskyou/ToolBench-X)
(arXiv 2606.25819, "Beyond Function Calling: Benchmarking Tool-Using Agents
under Tool-Environment Unreliability") into a suite of track `tool_use`. In each
task the agent answers one request by calling that task's own tools over MCP
and submits the exact answer.

## What upstream is

Checked against dataset commit `faaf5e8f7b71206c7fe60d015460dc5433fc0185`
(24 Jul 2026) and code commit `125c30611fbdf85d6e7bf5b4a22dceb5fa8da58d`
(github.com/Foreverskyou/ToolBench-X).

**Licence.** The GitHub code is MIT. The dataset card says: "ToolBench-X is
only used for academic research. Commercial use in any form is prohibited.
Without prior approval, you cannot distribute, publish, copy, disseminate, or
modify ToolBench-X in whole or in part." The imported suites copy and adapt the
data, so check this before using them anywhere public or commercial. The
importer's test fixture is synthetic, not upstream data.

- `tasks.zip`: `tasks/<type>/<topic>/<subtopic>.json`, each `{"tasks": [{id,
  user_prompt, tools_used, final_answer}]}`. `type` is `sequential`,
  `parallel` or `mixture`; in mixture tasks `tools_used` entries are
  `{"tool_name", "type"}`. The files list 2,610 tasks.
- `tools.zip`: `tools/<type>/<topic>/<subtopic>/task_N.py`, one Python module
  per task holding its tools. Modules exist for 1,106 tasks (378 sequential,
  358 parallel, 370 mixture), the paper's count; the other listed tasks have no
  tools and are skipped with that reason.
- `tools_exception.zip`: `tools_exception/<type>/<hazard>/<topic>/<subtopic>/task_N.py`
  plus `.hints.json`: the same tools with seeded fault injection, one of five
  hazard categories per task (Specification, Invocation, Execution, Output,
  Cross-Source Uncertainty).
- Upstream grades by exact match: `str(final_answer).strip() ==
  str(expected).strip()` (`agent/evaluation.py`).
- The tools first try public no-key web APIs (DuckDuckGo and similar) and fall
  back to deterministic data built into the module.

## Import

```sh
hf download Foreverskyou/ToolBench-X --repo-type dataset --local-dir datasets/toolbench-x/hf
cd datasets/toolbench-x/hf && mkdir x && for z in tasks tools tools_exception; do unzip -q $z.zip -d x/$z; done; cd -
R=faaf5e8f7b71206c7fe60d015460dc5433fc0185
bifrost-bench import toolbench-x -from datasets/toolbench-x/hf/x -suite toolbench-x -set ref=$R
bifrost-bench import toolbench-x -from datasets/toolbench-x/hf/x -suite toolbench-x-hazard -set variant=hazard -set ref=$R
bifrost-bench suite lock toolbench-x && bifrost-bench suite lock toolbench-x-hazard
```

| Setting (`-set k=v`) | Default | Meaning |
|---|---|---|
| `variant` | `clean` | `clean` serves `tools/`; `hazard` serves the `tools_exception/` twin with upstream's `strict_no_hint_profile` |
| `ref` | (required) | Dataset commit (hex), recorded as the tasks' `base_commit` |

`-from` takes the unzipped release (`tasks/tasks/...`) or a flat
`tasks/ tools/ tools_exception/` folder. Task ids are
`<seq|par|mix>-<topic>-<subtopic>-t<N>`, e.g. `mix-commerce-transactions-cart-and-checkout-issue-resolution-t1`.

## What a task is

```
tasks/<id>/
  task.yaml               service, exports, output kind state
  instruction.md          the request and the answer rules
  workspace/Dockerfile    the plain image the agent runs in (no upstream data)
  service/                Dockerfile and tbxsvc.py, identical for every task
  scenario/scenario.json  request, tool names, variant (no answer)
  scenario/tools.py       the task's upstream tool module
  verifier/tests/expected.json  the final_answer
  controls/               reference.sh, empty.sh, mutant1.sh, exploit.sh
```

**The service.** One task service `toolbench`, role `mcp`, proxied, port 8000:
`tbxsvc.py serve` loads `/task/tools.py` and serves the functions named in
`tools_used` as MCP tools (name, docstring, a JSON schema from the Python
signature), plus `submit_answer(answer)`. A tool exception is returned as an
MCP tool error (`Error: TimeoutError: ...`). Because the service is behind the
tool proxy, the bench's own tool faults apply on top. Every call and the answer
go to `/state/transcript.json` and `/state/answer.json`, both exported. The
service image is the same for all tasks of an import (`bench/svc-<suite>-toolbench:<hash>`);
the verifier runs in it.

**Offline tools.** Before loading the module the service points
`http(s)_proxy` at an unreachable address, so every live-API attempt fails at
once and the module's deterministic fallback answers. Runs are reproducible and
need no network; the live path upstream sometimes takes is never taken.

**Hazard variant.** The service applies the module's `strict_no_hint_profile`
through `set_injection_config`, as upstream's runner does without hints. The
injection is seeded (`_stable_rand` over task, tool, failpoint and attempt), so
the same call sequence meets the same faults. Hints (`*.hints.json`) are not
given to the agent.

**The verifier.** `tbxsvc.py verify` writes `{"reward": 0|1, "dimensions":
{"exact_match": 0|1}}`; no submission scores 0.

**Controls.** `reference` calls each listed tool with `user_prompt` (retrying
errors up to 3 times) and submits the expected answer; `mutant1` submits a
near miss (number + 1, or the text with a suffix); `exploit` submits the
request text without calling a tool; `empty` does nothing.

## Not comparable with the paper

- Tools run offline, on their fallback data only.
- The agent's prompt is the bench instruction, not upstream's
  model-driven loop (which also redacts answer-like fields and can retry with
  hints); there is no step limit beyond the run's wall budget.
- One upstream hazard module (`parallel/Output_Uncertainty/Commerce__Transactions/Cart_and_checkout_issue_support/task_2.py`)
  fails to load (`NameError: name 'dataclass' is not defined`); that task stays
  in `toolbench-x-hazard` and fails admission.
