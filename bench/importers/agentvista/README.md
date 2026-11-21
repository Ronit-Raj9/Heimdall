# agentvista importer

Imports [AgentVista](https://huggingface.co/datasets/Warrieryes/AgentVista)
(hkust-nlp, arXiv 2602.23166; dataset CC-BY-4.0) as one judged answer suite
on the research track: 209 questions over 1 to 10 images each, 7 domains and
25 subdomains.

- Each question is one answer task. Its images are copied to `repo/images/`;
  the agent may look at them, run Python in the workspace (upstream's code
  interpreter) and search the web with the research track's `websearch` MCP
  tool (`web_search`, `fetch_page`), which replaces upstream's paid Serper
  search and Jina reader. The Tavily cassette is empty, so search answers
  nothing unless an experiment opts into `external: live`.
- Grading: the judge grader with the `agentvista-v1` prompt, which applies
  AgentVista's verifier rule: the prediction (the last `<answer>...</answer>`
  when present) is correct when it matches the ground truth in meaning;
  synonyms and paraphrases count. Upstream uses gpt-4.1 as verifier; pin your
  judge in `suite.yaml` (written as `pin-me`).
- Drafts: the 9 questions of subdomain Visual Location Identification depend
  on upstream's reverse image search, which has no counterpart here. They are
  imported with tag `draft` and a `DRAFT.md` giving the reason, and are not
  meant to be admitted.
- The upstream code repository has no licence, so nothing is copied from it:
  the converter, instruction and judge prompt are the bench's own.

## Use

```sh
huggingface-cli download Warrieryes/AgentVista --repo-type dataset --local-dir av
python3 -I importers/agentvista/assets/extract.py av av/extracted   # needs pyarrow
bifrost-bench import agentvista -from av/extracted -suite agentvista -set revision=<sha>
# pin judge.provider / judge.model in benchmarks/agentvista/suite.yaml, then
bifrost-bench suite lint agentvista && bifrost-bench suite lock agentvista && bifrost-bench suite admit agentvista
```

Settings: `-set version=1.0.0`, `-set revision=<dataset commit>`.
