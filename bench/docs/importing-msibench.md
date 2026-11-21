# Importing MSI-Bench v2 (msibench)

Imports [MSI-Bench v2](https://github.com/boson-ai/MSI-Bench) (dataset
`v2/` of huggingface.co/datasets/M2cha4l1124/MSI-Bench, released 12 Nov 2026,
scoring update 18 Nov 2026) as the Heimdall voice suite `msi-bench-v2`.
Licence: CC BY-NC 4.0, non-commercial evaluation use only.

624 cases: 2 languages (en, zh) x 6 interaction patterns x 52, 2 to 4 human
speakers per case, synthesized room audio in 1,130 blocks.

Each case is a call. The voice caller (the voicebench caller image) plays
block 00 as the call's opening and each later block after the agent's spoken
reply to the previous one: MSI-Bench's own block protocol, with the agent's
live replies in place of the scripted assistant turns. After the last block
the caller hangs up and the agent writes its final reply plus
`Tool calls: [...]` to the answer file. The pinned judge (`grader: judge`,
prompt `qa-v1` rubric template) grades that answer against every atomic
criterion of the case's row in `index/rubrics.jsonl`; all must hold
(MSI-Bench's all-pass rate).

Deviations from upstream:
- Tool calls are reported by the agent in its answer and judged by the
  rubric's own `tool.call_args` criterion; upstream's deterministic validator
  (`eval.tool_validation`) is not run, since Heimdall's judge reads only the
  answer and the verifier has no network.
- Upstream's full-duplex streaming protocol (GPT Live 1 rows) is not
  replicated; every case is playable with the standard block protocol, so no
  case is a draft.
- The judge is the library's pinned judge, not upstream's gpt-6-luna; scores
  are not comparable to the leaderboard.

## Use

```sh
bifrost-bench import msibench -from <msi-bench-v2 dir> -suite msi-bench-v2 -set revision=<hf commit>
# pin judge.provider / judge.model in suite.yaml, then
bifrost-bench suite lock msi-bench-v2 && bifrost-bench suite admit msi-bench-v2
```

`-set languages=en` imports one language. Controls: the reference writes the
standard answer with the gold calls; the wrong answer (asks the room to
repeat, no calls), the empty answer and the exploit (asks the judge to pass
it) must fail.
