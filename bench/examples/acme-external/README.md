# Acme external example: an agent that calls a real HTTP API

This folder is a complete Heimdall (bifrost-bench) project for a company
whose agent must call an HTTP API outside the bench, here a weather
forecast service. It follows the external-api suite template
(`bifrost-bench suite init -template external-api`); the user guide is
[`docs/external-apis.md`](../../docs/external-apis.md). No Go code is needed.

- **One task.** `drone-window`: Acme Freight's dispatcher must decide which
  of three depots can fly drone deliveries tomorrow morning, from the
  forecast of each depot and the flight rule in the workspace.
- **The API is outside, the agent is not.** The agent calls
  `http://toolproxy:9200/x/weather/...` (the URL is in
  `BENCH_EXTERNAL_APIS`). The per-run tool proxy allows only `GET /forecast`
  and `GET /sites`, adds the API key (`ACME_WEATHER_KEY`, sent as
  `X-Api-Key`) that the agent never sees, and records every call in
  `tools.jsonl`.
- **Replay by default.** The task commits a cassette
  (`cassettes/weather.cassette.jsonl`): every run that does not opt in is
  answered from it, with no network, so every run sees the same forecasts
  and the result repeats. The proxy container gets no route out at all.
- **Controls.** A reference script that must pass, an empty control, a
  mutant that applies only half the rule, and an exploit that tries to leave
  the allowlist (an admin path, a POST, an absolute URL and a Host header
  naming another host, a `..` path) and fakes a result.
- **One tool probe.** `weather-error-first`: the first weather call answers
  an error; a good agent retries.

## What is fictional

Acme Freight, its depots, its dispatch rule and the weather service
(`api.weather.example`, a reserved name that never resolves) are invented
for this example. Everything here was written from scratch and is MIT
licensed (`provenance.license` in `task.yaml`, `created_by: hand-written
example`).

## Folder map

```
acme-external/
  README.md                         this file
  .gitignore                        runs/ and checks/ stay local
  deploy/bench.yaml                 paths, gateway, networks, helper and tool proxy images
  deploy/pricing-snapshot.json      model prices (copy of the bench's)
  policies/default.yaml             release policy (copy of the bench's)
  agents/oracle.yaml, noop.yaml     scripted controls for the free smoke run
  experiments/external-smoke.yaml   free smoke check, replay mode
  tools/standin.py                  local stand-in for the weather API, for live and record by hand
  benchmarks/acme-external/
    suite.yaml                      track tool_use, verifier_command with a reward
    probes/model-outage.yaml        gateway fault (copy of the bench's)
    probes/weather-error-first.yaml tool fault on the weather API's first call
    tasks/drone-window/
      task.yaml                     external: the weather API, allowlist, cassette
      cassettes/weather.cassette.jsonl
      repo/DISPATCH.md, repo/API.md what the agent starts with
      verifier/tests/verify.py      hidden verifier of plan.json
      solution/reference.sh         reference control
      controls/                     empty, mutant and exploit controls
```

## Run it

All from this folder, with `bifrost-bench` built from `bench/`
(`CGO_ENABLED=0 GOWORK=off go build -o bin/bifrost-bench ./cmd/bifrost-bench`)
and a tool proxy image (`images.toolproxy` in deploy/bench.yaml) built from
the same tree, so it knows external routes:

```
bifrost-bench suite lint acme-external
bifrost-bench suite lock acme-external
bifrost-bench suite admit acme-external
bifrost-bench plan experiments/external-smoke.yaml
BENCH_SERVER_MODE=synthetic bifrost-bench run experiments/external-smoke.yaml
```

Admission and the smoke experiment run in replay mode. Each run's
`tools.jsonl` shows the weather calls with `"protocol": "external"` and
`"replayed": true`; the exploit's calls show `"blocked": true`.

## Live and record mode without a real API

`tools/standin.py` serves the cassette's answers over plain HTTP on the
host and checks the `X-Api-Key` header, so live and record mode can be tried
end to end with nothing leaving the machine:

1. `ACME_WEATHER_KEY=demo-key python3 tools/standin.py --port 8765`
2. Copy this project to a scratch folder and, in the copy's
   `tasks/drone-window/task.yaml`, set
   `base_url: http://<this host's LAN address>:8765/v2` (a container on a
   bridge network reaches the host's LAN address; `127.0.0.1` would be the
   forwarder itself). Run `suite lock acme-external` in the copy.
3. Copy `experiments/external-smoke.yaml` to `external-record.yaml`, add
   `external: record`, and in the shell that runs the bench
   `export ACME_WEATHER_KEY=demo-key`. Plan and run it.
4. The stand-in prints `key=ok` for each call; the oracle run passes; its
   run folder holds `external/weather.cassette.jsonl` (no key in it) and
   `external/egress.json` naming the one allowed `host:port`; `tools.jsonl`
   rows have no `replayed`; the report header says
   `external APIs: record (not repeatable)`.
5. Copy the recorded cassette over the task's, stop the stand-in, lock and
   run the smoke experiment again: replay mode, no network, same result.

## Against a real API

To point the task at a real service, change `base_url`, the allowlist and
`auth_env` in `task.yaml`, then record a cassette once: copy the smoke
experiment, add `external: record`, set the key in the host environment
(`export ACME_WEATHER_KEY=...`), plan and run it with the oracle arm only.
The proxy then reaches the real host through a per-run forwarder that
connects to that one host and port, and writes
`runs/<exp>/<run>/external/weather.cassette.jsonl` (the key redacted). Copy
that file over the task's cassette and lock the suite again. Recording or
running live (`external: live`) costs you repeatability: the experiment is
labelled "not repeatable" in the report and kept off the leaderboard.
