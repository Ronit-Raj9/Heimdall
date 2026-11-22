# Monitoring, alerts and the status page

How to watch a hosted Heimdall: platform traces across the server, the
workers and the model gateway, Prometheus metrics with service level
objectives and alert rules, a Grafana dashboard, and the public status page
with incident notes. Everything here is about Heimdall itself, not about the
agents it evaluates: an evaluation's own trace stays in its run.

Files: `deploy/hosted/monitoring/` (Prometheus, alert and SLO rules, Grafana,
the OpenTelemetry Collector and Tempo), `bench/telemetry` (traces and the
optional metric push), `bench/statuspage` (the status page).

## Turn it on

In `deploy/hosted/.env`:

```sh
HEIMDALL_METRICS_LISTEN=0.0.0.0:9090                  # Prometheus scrape address, compose network only
HEIMDALL_OTEL_ENDPOINT=http://otel-collector:4318     # traces from the server and the shared worker
HEIMDALL_GRAFANA_PASSWORD=<a long random password>    # Grafana admin
HEIMDALL_STATUS_PROBES=gateway=http://bifrost:8080/health   # optional extra components on /status
```

Then start the profile next to the stack:

```sh
C="docker compose -f deploy/hosted/docker-compose.yml --env-file deploy/hosted/.env"
$C --profile monitoring up -d
$C restart heimdall            # picks up the OTEL_* and metrics settings
ssh -L 3000:127.0.0.1:3000 <vm>   # Grafana on http://localhost:3000, dashboard "Heimdall platform"
```

Nothing in the profile is published to the internet. Grafana listens on the
host's loopback only; Prometheus, the collector and Tempo are on the
internal networks.

Without the profile nothing changes: no endpoint means no span is made and
no request is sent.

## Traces

The server and every runner make OpenTelemetry spans and send them as
OTLP over HTTP (JSON bodies) to the endpoint the standard variables name:

| Variable | Meaning |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | base URL; `/v1/traces` (and `/v1/metrics`) are added |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | full traces URL, wins over the base |
| `OTEL_EXPORTER_OTLP_HEADERS` | `key=value,key2=value2`, for a hosted backend's API key |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | `always_on` (default), `always_off`, `traceidratio` with a ratio; children follow their parent |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` | resource names; compose sets `heimdall-server` and `heimdall-shared-runner` |
| `OTEL_METRICS_EXPORTER=otlp` | also push metrics over OTLP (off by default; Prometheus scrapes) |
| `OTEL_SDK_DISABLED=true` | everything off |

Only `http/json` is spoken (an OpenTelemetry Collector accepts it on port
4318). The exporter needs no extra Go module. It batches spans every 5
seconds, keeps at most 4,096 queued, and drops (and counts) spans rather than
slow a request when the collector is away
(`heimdall_telemetry_spans_total{result}`).

One trace per evaluation. Its trace id is derived from the org and
experiment id, so the server, every worker and the gateway calls land on
the same trace with no shared state:

```
experiment                      server, one span per time the evaluation opens to workers
  lease                         one run attempt on a worker (slot, task, agent version, worker, attempt)
    worker.slot                 the worker's whole handling of it
      run                       the run itself (run id, outcome)
        (judge calls)           Bifrost spans, when the gateway traces
      upload                    upload and complete
        POST worker             the server's request spans for that lease
```

The lease answer carries a W3C `traceparent`; the worker sends it on every
heartbeat, upload and complete call, and on judge calls to Bifrost. Every
other API request gets its own server span named by method and area.

Span attributes may hold ids (org, experiment, slot, run, lease, worker).
Metric labels never do. Prompt, answer and tool content never goes into
platform telemetry.

The collector (`monitoring/otel-collector.yaml`) keeps every trace with an
error, every trace slower than 5 seconds and 10% of the rest, then sends them
to Tempo (7 days). Find an evaluation's trace in Grafana Explore on Tempo by
the attribute `heimdall.experiment`, or a run's by `heimdall.run`.

### The gateway

Bifrost has its own OTel plugin and honours an incoming `traceparent`. To
put the shared gateway's spans on the same traces, add a plugin entry to
`deploy/bifrost/config.json` (or through Bifrost's API):

```json
"plugins": [
  {
    "enabled": true,
    "name": "otel",
    "config": {
      "service_name": "heimdall-gateway",
      "collector_url": "http://otel-collector:4318/v1/traces",
      "trace_type": "genai_extension",
      "protocol": "http",
      "group_traces_by_session": true,
      "disable_content_logging": true
    }
  }
]
```

`disable_content_logging` keeps prompts out. Agent calls carry the run id as
the Bifrost session id, so `group_traces_by_session` groups each run's model
calls even when the agent itself sends no `traceparent`.

## Metrics

`http://heimdall:9090/metrics` (see docs/hosting.md, Operating it) has, in
addition to the families listed there:

| Metric | What |
|---|---|
| `heimdall_remote_leases_granted_total` | runs handed to workers |
| `heimdall_remote_leases_ended_total{reason}` | run attempts ended: accepted, rejected, lost, released, refused, revoked, stopped, other |
| `heimdall_evaluation_start_seconds` | histogram: evaluation opening to workers until its first run starts |
| `heimdall_remote_complete_seconds{result}` | histogram: time the server takes to verify, store and seal an upload |
| `heimdall_telemetry_spans_total{result}` | spans sent, dropped or failed (only when tracing is on) |

A hosted org's coordinator is dropped when the org goes idle, so these
counters can reset; every rule uses `rate` or `increase`, which handle that.

## Service level objectives

Defined in `monitoring/slos.yml` (Sloth format, the source of truth) and
turned into recording rules and multiwindow burn-rate alerts in
`monitoring/slo-rules.yml`: page when the 1 hour and 5 minute windows burn
the 30-day budget 14.4 times too fast (or 6 hours and 30 minutes at 6
times), ticket at 3 times over 1 day and 2 hours (or 1 times over 3 days and
6 hours). `deploy/monitoring_test.go` checks that both files name the same
SLOs and only metrics the server exports.

### SLO api-availability

99.5% of API requests (health probes and the static site left out) do not
fail with a 5xx. When it burns: find the failing area on the dashboard
("Requests per second by area"), read `$C logs --since 30m heimdall`, check `/readyz`
(database and migrations), then the Postgres container.

### SLO evaluation-start-latency

95% of evaluations start their first run within 5 minutes of opening to
workers. When it burns: workers are missing or full. Check the dashboard's
"Workers by state" and "Evaluations waiting to start", the shared worker
(`$C --profile shared-runner ps`), its slots (`HEIMDALL_SHARED_SLOTS`) and
disk, and org experiment limits.

### SLO run-continuity

99% of run attempts handed to a worker are not lost. A lost run is queued
again and charged at its estimate. When it burns: a worker host is
overloaded, restarting or losing its network. See HeimdallRunsLost below.

### SLO upload-acceptance

99% of finished runs are accepted on upload. A rejection repeats the run.
When it burns: see HeimdallUploadsRejected below.

### SLO time-to-verdict-step

99% of uploads are verified, stored and sealed within 30 seconds. When it
burns: the server's disk or CPU is saturated (a large evaluation's evidence
mirror, a backup), or the runs volume is slow. Check host disk and IO.

## Alerts

`monitoring/alerts.yml`, on the existing metrics. Severity `page` wakes
someone, `ticket` is for working hours, `info` is a dashboard note. Route
them with an Alertmanager of your own (not part of the profile); each rule
links its section below.

### HeimdallDown

Prometheus cannot scrape the server. `$C ps heimdall`, `$C logs --tail 200
heimdall`. If the server restarts in a loop, `/readyz` says which check
fails. Check `HEIMDALL_METRICS_LISTEN` is still set.

### HeimdallHighServerErrors

Over 5% of API requests fail with a 5xx for 5 minutes. As for the
api-availability SLO. Post an incident on the status page if users are
affected (see below).

### HeimdallSlowAPI

API p95 above 2 seconds for 10 minutes (live streams left out). Usually a
large evaluation reading or the database. Check Postgres CPU and the
dashboard's latency by area.

### HeimdallHeartbeatLate

A running evaluation's worker has not reported for over 90 seconds; at 2
minutes the run is lost. Find the worker in the trace (`heimdall.worker`)
or on the Workers page, and check its host.

### HeimdallNoWorkers

Evaluations are queued and no worker is connected, for 10 minutes. Start
the shared worker (`$C --profile shared-runner up -d shared-runner`) or ask
the org to start its own. Post an incident on the `workers` component.

### HeimdallQueueBacklog

More than 50 evaluations waiting for 15 minutes. Add worker slots or hosts,
or raise experiment limits if workers sit idle.

### HeimdallWorkersDiskLow

Workers refuse new runs because their disk is low. On the worker host:
`docker system df`, then the runner's own sweep (docs/hosting.md, Runner
disk); never prune images a suite pins.

### HeimdallRunsLost

More than 3 runs lost in 15 minutes. Each was queued again and charged at
its estimate. Look at the lease spans with error status in Tempo: the
`heimdall.worker` attribute names the worker.

### HeimdallUploadsRejected

More than 3 uploads rejected in 15 minutes. The server log says why
(`remote upload rejected`); a worker on an older version or a changed
suite lock is the usual cause. Upgrade the worker.

### HeimdallTelemetryDropping

Spans are dropped or fail to send. Evaluations are not affected. Check the
collector (`$C --profile monitoring logs otel-collector`).

## The status page

`https://<your domain>/status` is public: no sign-in, no org. It shows each
component's health and the operator's incident notes, refreshes every 30
seconds, and is also served as JSON at `GET /api/bench/status` for uptime
tools.

Components and their live checks (run at most once per 15 seconds however
often the page is read):

| Id | Name | Check |
|---|---|---|
| `api` | Website and API | the server answers |
| `storage` | Run storage | the runs folder is writable |
| `database` | Database | the database answers and migrations are current (hosted) |
| `workers` | Evaluation workers | workers by state, the oldest heartbeat (over 90 s degrades), low disks; "no data" with no worker connected |
| `queue` | Evaluation queue | more than 50 evaluations waiting degrades |
| any id in `HEIMDALL_STATUS_PROBES` | `gateway` reads "Model gateway" | HTTP GET: 2xx is fine, slower than 2 s degrades, anything else is an outage |

The page never shows an error text, a host name or anyone's data: each
component gets a fixed short sentence.

### Incidents

Write incident notes from the server container. They show within seconds;
nothing restarts:

```sh
$C exec heimdall bifrost-bench status incident open \
  -title "Evaluations start late" -impact major -components workers,queue \
  -message "Evaluations are waiting longer than usual to start. We are looking into it."
$C exec heimdall bifrost-bench status incident list
$C exec heimdall bifrost-bench status incident update -id inc_... -status identified \
  -message "A worker host ran out of disk. Adding space."
$C exec heimdall bifrost-bench status incident resolve -id inc_... -message "All evaluations are running again."

# Planned maintenance, announced ahead (shown, not applied until it starts):
$C exec heimdall bifrost-bench status incident open -title "Database upgrade" \
  -scheduled-for 2026-12-01T02:00:00Z -components database \
  -message "About ten minutes of read-only time."
```

Impact sets the level of the named components while the incident is open:
`minor` degraded, `major` partial outage, `critical` major outage,
`maintenance` maintenance, `none` a note only. No `-components` means the
whole platform. Resolved incidents stay listed for 14 days. `remove` deletes
a note posted by mistake.

The notes live in `HEIMDALL_STATUS_FILE` (compose:
`/var/lib/heimdall/status/incidents.json` on the data volume, so backups
include it). Text is shown publicly as plain text.

## Not covered yet

- A synthetic scripted evaluation every 10 minutes feeding the status page.
- Alertmanager routing (Slack, PagerDuty) for the platform's own alerts.
- Host disk and CPU of the server VM (add node_exporter to Prometheus).
- Evidence mirror lag as an SLO.
