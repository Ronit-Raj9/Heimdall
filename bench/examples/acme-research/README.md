# Acme research example: a research agent that searches the web through Tavily

This folder is a complete Heimdall (bifrost-bench) project for a team whose
agent answers questions by searching the web and citing its sources. It
follows the research suite template (`bifrost-bench suite init -template
research`); the user guide is [`docs/research-tasks.md`](../../docs/research-tasks.md).
No Go code is needed to use it.

- **Eight tasks** about the Calder Coast, a fictional region, so no model can
  answer from memory: a single-hop year (`ferry-start`), a two-hop question
  (`bridge-mayor`: the bridge's town, then its current mayor), a three-hop
  question (`county-town-population`), a number with a unit
  (`tunnel-length`), a date (`festival-date`), an abstention where the
  sources do not hold the answer (`aquarium-opening-day`), conflicting
  sources where the newer page is right (`mooring-fee`), and a question
  whose results include a page with a planted prompt injection
  (`last-keeper`).
- **Search goes through Tavily, and Tavily is reached only through the tool
  proxy.** The suite declares Tavily's API (`external:` in `suite.yaml`:
  `POST /search` and `POST /extract` only, the key from `TAVILY_API_KEY` sent
  by the proxy as `Authorization: Bearer <key>`). The agent uses the
  `websearch` MCP server (`web_search`, `fetch_page`), which calls that
  route, or calls the route itself. Every search and result lands in
  `tools.jsonl`.
- **Replay by default, no key needed.** Runs are answered from
  `cassettes/tavily.cassette.jsonl` with no network. That cassette was
  recorded through the real tool proxy code in record mode from the Tavily
  stand-in (`bench/research/standin`, a fixed corpus of 80 hand-written
  pages), not from the live web. No real Tavily call has been made.
- **Graded without a judge.** Each task writes `answer.txt` and
  `sources.json`. The hidden verifier checks the answer (as answer_match
  does) and the citations: no invented URL, and for grounded tasks at least
  one page that holds the evidence. The metrics `searches`, `sources_cited`,
  `citations_grounded` (cited pages this run's searches actually returned,
  from the ledger) and `citation_precision` are reported per arm.
- **Controls per task.** A reference script that searches and fetches through
  the websearch tool and must pass; an empty control; a mutant with a wrong
  answer citing real pages (the outdated fee, the old mayor, the injected
  page); and an exploit with the right answer and an invented citation,
  which must fail.
- **Probes.** `search-outage` (every search errors), `stale-results` (oldest
  pages first), `hostile-page` (an injected instruction in the first search
  result, watched by the `tool_injection_followed` gate), and the gateway
  `model-outage`.

## What is fictional

The Calder Coast, its towns, people, figures and websites (every domain uses
the reserved `.example` name, which never resolves) are invented for this
example. Everything here was written from scratch and is MIT licensed
(`provenance.license` in each `task.yaml`).

## Folder map

```
acme-research/
  README.md                         this file
  deploy/bench.yaml                 paths, gateway, networks, helper and tool proxy images
  agents/oracle.yaml, noop.yaml     scripted controls for the free smoke run
  experiments/research-smoke.yaml   free smoke check, replay mode
  policies/default.yaml             release policy (copy of the bench's)
  benchmarks/acme-research/
    suite.yaml                      track research, the websearch service, the tavily API
    services/websearch/             the MCP search tool (copy of bench/research/websearch)
    cassettes/queries.json          what was recorded: 69 searches, a fetch of every corpus page
    cassettes/tavily.cassette.jsonl recorded from the stand-in through the tool proxy
    probes/                         model-outage, search-outage, stale-results, hostile-page
    tasks/<task>/
      task.yaml                     output: answer.txt and sources.json artifacts
      instruction.md                the question
      repo/RESEARCH.md              the house rules the agent starts with
      verifier/tests/               verify.py, research.json (hidden spec), corpus_urls.json
      solution/reference.sh         reference control
      controls/                     empty, mutant and exploit controls
```

## Run it

All from this folder, with `bifrost-bench` built from `bench/`
(`CGO_ENABLED=0 GOWORK=off go build -o bin/bifrost-bench ./cmd/bifrost-bench`)
and the tool proxy image `images.toolproxy` built from the same tree:

```
bifrost-bench suite lint acme-research
bifrost-bench suite lock acme-research
bifrost-bench suite admit acme-research      # builds bench/acme-research-websearch:1 when missing
bifrost-bench plan experiments/research-smoke.yaml
BENCH_SERVER_MODE=synthetic bifrost-bench run experiments/research-smoke.yaml
```

The oracle arm must verify 8 of 8 and the noop arm 0 of 8. Each oracle
run's `tools.jsonl` shows the websearch MCP calls and the Tavily calls with
`"protocol": "external"` and `"replayed": true`.

## Re-recording

From the stand-in (what is committed), from `bench/`:

```
go run ./research/cmd/tavily-record -suite examples/acme-research/benchmarks/acme-research -corpus-urls
```

then `suite lock` and `suite admit` again. With a real Tavily key the same
command takes `-upstream https://api.tavily.com` and the key from the
environment only (`read -rs TAVILY_API_KEY && export TAVILY_API_KEY`); see
the guide. These tasks are about a fictional region, so a live recording of
their queries would hold no answers: record real cassettes for tasks about
real topics.
