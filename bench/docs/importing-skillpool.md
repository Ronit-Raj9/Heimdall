# Importing the Skill-Usage setting (34k-skill pool)

`bifrost-bench import skillpool` reproduces the setting of "How Well Do Agentic
Skills Work in the Wild" (arXiv 2604.04323,
https://github.com/UCSB-NLP-Chang/Skill-Usage, commit `03446d1`, data
https://huggingface.co/datasets/Shiyu-Lab/Skill-Usage) on the tasks of a
Harbor suite that is already imported (SkillsBench as `hd-harbor-skills`,
Terminal-Bench 2.0 as `hd-harbor-tb2`). Licence: not stated upstream (no
LICENSE file, no Hugging Face licence tag).

## What it writes

- The base suite's tasks, hard-linked, same ids and content.
- Per task, `skill-sets/<set>/`, upstream's per-task skill folders:

  | Set | SkillsBench folder | Terminal-Bench folder |
  |---|---|---|
  | `curated` | `skillsbench/curated` | none |
  | `retrieved` | `skillsbench/retrieved_w_curated` (top 5 from the pool, curated in it) | `terminalbench/retrieved` |
  | `retrieved-wo-curated` | `skillsbench/retrieved_wo_curated` | none |
  | `pool` | upstream `data/find-skills`, pointed at the pool service | same |

- Suite service `skills-pool` (`http://skills-pool:8742`, image
  `bench/skills-pool:1`, build context `services/skills-pool`): upstream's
  `search_server` with the BM25 index `skills.db` and every `SKILL.md` of the
  34k pool. `/keyword` and `/detail/{id}` work; `/semantic` and `/hybrid` need
  Qwen3-Embedding-4B (8 GB per run) and are not served.
- `experiment-skills-pool.yaml`: arms no skills (`skills.enabled: false`),
  curated (`skills.set: curated`), retrieved, retrieved without curated, and
  pool.

An agent config chooses a set with `skills.set: <name>`: the task folder
`skill-sets/<name>` replaces the task's `skills` folder; a task without that
folder installs no task skills (see [skills.md](skills.md)).

## Running it

```
# dataset: git clone https://github.com/UCSB-NLP-Chang/Skill-Usage <ds>/repo
# hf/: skills-34k/skills.zip, search_index/search_index.zip and
#      <bench>/<setting>/<task>/skills/** ; unzip skills.db and */SKILL.md into hf/unz
bifrost-bench import skillpool -from <ds> -suite hd-harbor-skills-pool \
  -set base=benchmarks/hd-harbor-skills -set benchmark=skillsbench
bifrost-bench import skillpool -from <ds> -suite tb2-skills-pool \
  -set base=benchmarks/hd-harbor-tb2 -set benchmark=terminalbench
docker build -t bench/skills-pool:1 benchmarks/hd-harbor-skills-pool/services/skills-pool
bifrost-bench suite lock hd-harbor-skills-pool
```

The paper's own harness (Harbor with Claude Code, Terminus-2, Qwen-Code) and
its scores are not reproduced; the task, the skills and the verifier are
upstream's.
