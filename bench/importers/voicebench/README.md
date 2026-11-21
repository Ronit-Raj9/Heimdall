# voicebench importer

Imports [VoiceBench](https://huggingface.co/datasets/hlt-lab/voicebench)
(Apache-2.0; paper arXiv 2410.17196; graders from
github.com/MatthewCYM/VoiceBench at b5615417) as a Heimdall voice suite.

Each item is one call. The voice caller (`services/customer`, built from
bench/voice) plays the item's own recorded spoken question (the task's
`audio/question.wav`, through the new `audio` map of `user.json`), transcribes
the agent's spoken answer with Whisper tiny.en on CPU, says thank you and
hangs up. The hidden verifier grades every agent line after the question with
VoiceBench's own deterministic rule (`assets/vbeval.py`, vendored):

| Subset | Items | Rule |
|---|---|---|
| openbookqa | 455 | `MCQEvaluator.extract_answer` equals the reference letter |
| mmsu | 3,074 | same, 12 MMLU-Pro categories |
| bbh | 1,000 | `BBHEvaluator.extract_answer` (navigate, sports, hyperbaton, web of lies) |
| advbench | 520 | `HarmEvaluator.evaluate_single`: the answer refuses |

Deviations from upstream, all so a grade is deterministic and per item: an
answer VoiceBench cannot parse fails (upstream guesses at random); silence
fails (upstream counts an empty advbench answer as a refusal); the refusal
phrases are compared lower-cased on both sides (upstream lower-cases only the
answer, so its mixed-case phrases never match).

Left out: ifeval (instruction checkers not ported) and the GPT-judged
open-ended subsets (alpacaeval, commoneval, wildvoice, sd-qa, mtbench).

## Use

```sh
# once: parquet -> items.jsonl + wav files (needs pyarrow)
python3 importers/voicebench/assets/extract.py <voicebench-download> <extracted>
bifrost-bench import voicebench -from <extracted> -suite voicebench -set revision=<hf revision>
docker build -t heimdall-library/voicebench-caller:1 benchmarks/voicebench/services/customer
bifrost-bench suite lock voicebench && bifrost-bench suite admit voicebench
```

`-set subsets=openbookqa,bbh` imports some subsets only. Controls per task:
the reference speaks the right answer in tone code, the mutant a wrong one,
empty never calls, exploit plants a transcript and reward files.

Agents: any heimdall-voice/1 agent. `voice-local-cascade` (faster-whisper
base.en, the run's model through Bifrost, Piper) and
`voice-cascaded-sarvam-qa` are the library's configs for this suite.
