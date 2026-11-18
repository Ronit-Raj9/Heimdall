# Extensions wave (X1-X8): design contract

Status: built 18 Nov 2026. Eight capabilities added in parallel. This file
fixes the names and ownership that let the work fit together. Where it and
the code differ, the code wins.

## Placement

The wave ran in two working copies and was merged afterwards: X2, X3, X4,
X5b and X7 on the `agents-safety` branch (they build on team runs, the
safety catalogue and the compliance mapping); X1, X5a, X6 and X8 on the
`adoption` branch (they build on judge calibration, the research and
support tracks, the failure inbox and the drift canary).

## Rules that do not bend

1. Additive schemas: new fields are `omitempty` or `omitzero`, so every
   manifest, lock, seal and recorded run keeps its hash. Hash tests in
   `engine` and `suite` stay green.
2. Only witnessed evidence decides a verdict. Judged grades (vision judge,
   MARBLE's judge), transcripts, imported traces and self-reports are
   labelled with their strength and cannot decide a verdict on their own.
3. No real provider or third-party call in any test, and no paid call at
   all. Prove everything with scripted models, fakes, local models and
   recorded fixtures at zero cost. Where a real key or a real account would
   be needed, the code path is tested against a local fake that speaks the
   same API, and the doc says no real call was made.
4. Unsupported combinations are refused at plan time with a reason, never
   run half-way (Kubernetes without services, GPU task on a machine without
   one, a compose file with privileged mode).
5. Statistics stay honest: anything that looks at results repeatedly
   controls its false-alarm rate and says how; otherwise it reports and
   does not decide.
6. The local single-user flow keeps working with no database and no login.
7. Upstream facts (dataset formats, licences, API shapes, Bifrost
   behaviour) are verified from the primary source and dated in the doc,
   or marked unverified.

## Names

Tracks (`schemas/scenario.go`):

| Constant | Value | Owner |
|---|---|---|
| `TrackVoice` | `voice` | X8 |
| `TrackMLEngineering` | `ml_engineering` | X7 |

Commands:

| Verb | Owner |
|---|---|
| `task init -from-compose <file>`; Harbor import of compose tasks | X2 |
| `k8s check`, `k8s gateway up|down` | X3 |
| `codescan diff <run>` (and `-file <diff>`), `codescan rules` | X4 |
| `import gaia`, `import browsecomp-plus` | X5a |
| `import marble` | X5b |
| `livescore init|pull|status|compare` | X6 |
| `gpu check`; `import kernelbench` | X7 |
| `voice check` | X8 |

Schema additions (all optional; each owner adds its spec JSON schema too):

| Where | Field | Owner |
|---|---|---|
| task / scenario | `Compose string` (path inside the task folder) | X2 |
| config (`lib/config.go`) | `environment.driver: docker|kubernetes`, `kubernetes: {...}` | X3 |
| experiment | `Environment *EnvironmentChoice{Driver}` | X3 |
| task | `Resources *Resources{GPU *GPURequest{Count, MinVRAMMB}}` | X7 |
| config | `gpu: {mode: cdi|device_requests, devices: [...]}` | X7 |
| run record | `Hardware *HardwareRecord{GPUs []GPUInfo}` | X7 |
| output (`OutputSpec`) | `Visual *VisualCheck{Capture, Reference, Method, Threshold, Tolerance, Masks, Checks, Rubric, Judge}`; the grade gets `Grade.Visual *VisualGrade`; `CalibrationItem.Image` (sha256 of a labelled image) | X1 |
| task | `Voice *VoiceSpec{Caller, Transport, Probes}` | X8 |
| experiment | `CodeScan *CodeScanPolicy{VetoAt, Ignore}` (`codescan`) | X4 |
| suite | `CodeScan *SuiteCodeScan{External []CodeScanner}` (`codescan`) | X4 |
| evidence | `CodeScan *CodeScanEvidence` (`codescan` in evidence.json) | X4 |

Gates, instruments and grade sources:

| Name | Kind | Owner |
|---|---|---|
| `insecure_code_added` | baseline gate, report by default, veto by experiment `codescan.veto_at` (release policies block on its metrics) | X4 |
| `hardware_parity` | verdict instrument: arms on different GPU models make the verdict INVALID | X7 |
| `visual` | grade source, deterministic image checks | X1 |
| `visual_judge` | grade source, judged, needs calibration to decide | X1 |
| `live` | evidence class for live-traffic scores; never mixed into experiment verdicts | X6 |

X6 also adds (code, 18 Nov 2026): alert events `live_signal_threshold` and
`live_comparison_worse` (`alerts/webhook.go`, marked), the schedule job
`livescore-pull` (`schedule/file.go`, marked), and the routes
`GET /livescore`, `GET /livescore/sessions`, `GET /livescore/compare`,
`POST /livescore/pull` (write token). No metric, no schema field: live
scores are not run metrics.

Metrics (owner adds the descriptor, the row in `verdict/direction.go` and a
test row in `metrics/builtin_test.go`; the direction-table test in
`cmd/bifrost-bench` must pass):

| Metrics | Owner |
|---|---|
| `insecure_findings`, `insecure_findings_high`, `secrets_added` | X4 |
| `gpu_seconds`, `peak_vram_mb`, `gpu_util`, `speedup`, `heldout_score`, `time_to_target_s` | X7 |
| `visual_score`, `visual_judge_score` | X1 |
| `time_to_first_audio_ms`, `turn_latency_p50_ms`, `turn_latency_p95_ms`, `barge_in_handled`, `dead_air_s`, `stt_wer`, `voice_cost_per_minute` | X8 |

Compliance and safety: every new gate and probe gets a row in the S2
compliance mapping and the S1 safety catalogue where those exist in your
copy (repo-ma has both). A mapping test may enforce this.

## Ownership

| ID | Packages and files |
|---|---|
| X1 | `bench/visual/`, `graders/visual*.go`, visual examples under `examples/acme-design/` or a new `examples/acme-visual/`, `docs/visual-grading.md`, `ui/components/visual/` |
| X2 | `bench/compose/`, compose handling in `importers/harbor/` (marked), `docs/multi-container.md`, an example `examples/acme-stack/` |
| X3 | `environments/driver*.go` (the seam), `bench/k8s/`, `deploy/k8s/`, `docs/kubernetes.md` |
| X4 | `bench/codescan/`, `gates/insecurecode.go`, `metrics/codescan.go`, `docs/code-scan.md`, `ui/components/codescan/`, plus new files `schemas/codescan.go`, `graders/codescanroom.go`, `handlers/runcodescan.go`, `cmd/bifrost-bench/codescan.go`, `ui/lib/store/apis/codescanApi.ts`, `engine/regatecodescan_test.go` |
| X5a | `importers/gaia/`, `importers/browsecompplus/`, `docs/importers-research.md` |
| X5b | `importers/marble/`, `docs/importers-marble.md` |
| X6 | `bench/livescore/`, `cmd/bifrost-bench/livescore.go`, `handlers/livescore.go`, `docs/live-scoring.md`, `ui/components/livescore/`, `ui/app/live/` |
| X7 | `bench/gpu/`, GPU parts of `environments/` (marked), `metrics/gpu.go`, `examples/acme-gpu/`, `importers/kernelbench/`, `docs/gpu-tasks.md` |
| X8 | `bench/voice/`, `metrics/voice.go`, `examples/acme-voice-desk/`, `scaffold/templates/suites/voice/`, `docs/voice-agents.md`, `docs/voice-protocol.md`, `ui/components/voice/` |

Shared files (`schemas/*`, `spec/*`, `metrics/builtin.go`,
`verdict/direction.go`, `handlers/openapi.go`, `server/server.go`,
`cmd/bifrost-bench/serve.go`, `cmd/bifrost-bench/app.go`, `engine/planner.go`,
`README.md`, `ui/lib/types/bench.ts`, `ui/locales/en/bench.json`,
`ui/lib/constants/navigation.ts`): additive edits only, re-read right before
each edit, one marked line or block per feature (comment `X<n>`).

## UI rule

Each feature with something to show ships TypeScript types, an RTK Query
hook, contract key files, and one self-contained page or panel under
`ui/components/<area>/` with vitest tests, reachable from one navigation
entry or from the run or verdict page. Do not restructure shared UI.
