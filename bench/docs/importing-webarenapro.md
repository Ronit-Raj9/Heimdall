# Importing WebArena-Pro

`bifrost-bench import webarenapro` turns WebArena-Pro's native Harbor export into a browser suite. The code is `importers/webarenapro/`.

WebArena-Pro (McGill-NLP, arXiv 2611.17955, MIT): 400 tasks (dev 100, test 300) on 20 self-hosted web apps (acidwave, erp-next, firefly, flight-search, immich, jobportal, mattermost, metabase, minerva-lite, mydrive, nextcloud, notion, password-manager, penpot, peppermint, pipedrive, thunderbird, travelmap, x-lite, yt-lite). Imported at commit `42f4a0763cd7d451cfa8872c0cdd62c34485713f` (18 Nov 2026).

## Fetch and export

```sh
git clone https://github.com/McGill-NLP/webarena-pro src && cd src && git checkout 42f4a07
make harbor-export          # writes <out>/dev and <out>/test, each with manifest.json
docker build ... (webarena-pro-harbor)   # the runtime image; tag it as the manifest's runtime_image
docker pull webarenapro/<site>:latest     # one image per app; the tasks pin them by digest
```

## Import

```sh
bifrost-bench import webarenapro -from export -suite webarena-pro -set revision=<commit>
```

Settings: `split` (dev or test; default both), `judge_model` (default `gpt-6.2-sol`, `off` drops the gateway), `judge_provider` (default `openai`), `runtime_image` (overrides the manifest), `revision`, `agent_timeout`.

## Mapping

| Upstream (Harbor compose) | Bench |
|---|---|
| service `webarena` (runtime: browser + WebArena-Pro MCP server + grader) | service `webarena`, role mcp, port 8000, `/mcp`, proxied: every browser action is a ledger row |
| one service per website, on an internal network | hidden data service per site (image pinned by digest); only the runtime reaches it |
| `environment/browser/episode.json` | `seed/episode.json`, copied to `/opt/webarena/episode.json` |
| `[[verifier.collect]] webarena-pro-harbor finalize` | the `webarena` service's export `result.json` (finalize, then `cat /webarena-evidence/result.json`) |
| `tests/verify.py` | `verifier/tests/verify.py` (same checks), reward = the runtime's partial-credit reward, pass at 1 |
| judge env (`OPENAI_BASE_URL`, `WEBARENAPRO_LLM_JUDGE_MODEL`) | service gateway (`gateway:` on `webarena`), mapped from `BENCH_SVC_GATEWAY_*` |
| `WP_EVAL_TOKEN` | a fixed per-task value derived from the task (both services) |

Each run starts its own app containers from the pinned images, so every task begins from the image's seeded state: the per-task reset is the fresh container. The runtime grades with its tiered rubric: tier 0 `answer_match`/`backend_state` decide alone; other nodes fall through to the llm_judge tier, which needs the gateway.

## Controls

Upstream ships no reference solutions. When every rubric node has an `answer_match` tier the importer writes a reference that calls `webarena_submit` with the expected text (29 of 400 tasks); every other task's reference refuses, so admission leaves it a draft. The empty control submits nothing; the exploit plants a fake evidence file and reward in the workspace.
