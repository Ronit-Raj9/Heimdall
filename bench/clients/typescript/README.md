# @heimdall/bench-client

Thin TypeScript client for the Heimdall bench API (`/api/bench`). Zero runtime dependencies: it uses the global `fetch` of Node 18+ and browsers. ESM, strict types. The wire types in `src/types.ts` are a generated copy of the UI's `bench/ui/lib/types/bench.ts`, which mirrors the Go types; `node scripts/sync-types.mjs` refreshes it.

```ts
import { Client } from "@heimdall/bench-client";

const c = new Client("http://127.0.0.1:8090", { token: process.env.BENCH_WRITE_TOKEN });
const started = await c.runExperiment(yamlText);          // YAML text, or an object sent as JSON
const status = await c.waitForExperiment(started.exp_id, { timeoutMs: 3_600_000 });
const result = await c.check(started.exp_id);
const [decision, action, policy] = result;
if (result.blocked) process.exit(1);
```

## Public API v1 (0.2.0)

`V1Client` speaks the versioned public API (`/api/v1`, `docs/api-v1.md`): agents and versions, compatibility, benchmarks, evaluations, estimates, report cards, the agent leaderboard, runs and trace export. On a hosted server pass an organization API token whose scope (`read`, `evaluate`, `admin`) covers the calls; `evaluateAndWait` needs `evaluate`.

```ts
import { V1Client, armScore } from "@heimdall/bench-client";

const hd = new V1Client("https://heimdall.example.com", { token: process.env.HEIMDALL_TOKEN, requireLive: true });
const spec = { name: "nightly", mode: "score" as const, pack: "support", arms: [{ label: "A", agent: "support-bot", model: "gpt-5.1" }] };
const estimate = await hd.estimateEvaluation(spec);
console.log(estimate.runs, estimate.est_cost_usd);

const { succeeded, report } = await hd.evaluateAndWait(spec, { timeoutMs: 4 * 3_600_000, onStatus: (ev) => console.log(ev.status) });
console.log(succeeded, armScore(report, "A"));
for (const b of report.benchmarks) console.log(b.benchmark, b.arms.A?.score, b.arms.A?.ci_low, b.arms.A?.ci_high);

const { runs } = await hd.runs({ limit: 1 });
if (runs[0]) console.log((await hd.trace(runs[0].run_id, "jsonl")).text());
```

`evaluateAndWait` cancels the evaluation when it times out (`cancelOnTimeout: false` keeps it) and rejects with `HeimdallTimeout`; a failed or cancelled evaluation still resolves, so check `succeeded`. Report cards come back normalized (`normalizeReportCard`): every list and per-arm metric is present.

Benchmarks as code (`docs/datasets-as-code.md`): `createBenchmark(id)`, `setGrader(id, grader, judge)`, `upsertTasks(id, rows, { mode, match, source, dryRun })` (each change a new version; the personal data policy runs first), `datasetTasks(id)`, `deleteTask(id, task)`, `datasetVersions(id)`, `datasetVersion(id, n)`, `dataPolicy(id)`, `setDataPolicy(id, { pii, access })` and `importFromPlatform({...})` (LangSmith, Langfuse, Braintrust, Datadog, with the org's secrets).

Every JSON response carries the server `mode` (`live`, `recorded` or `synthetic`); raw downloads (`runPatch`, `runFile`, `releaseEvidence`) resolve to a `RawResponse` whose `mode` comes from `X-Bench-Mode`. `{ requireLive: true }` rejects every answer that is not live with `ModeError`.

Agent images can import `FaultSession` and wrap tool, memory, browser or retry
callbacks. The wrapper is pass-through without `BENCH_FAULT_*`; configured
control-plane failures throw `FaultControlError`.

`token` is sent as `Authorization: Bearer <token>`: the server requires its write token (`BENCH_WRITE_TOKEN`) on every POST route; reads need none. The worker routes (`BENCH_WORKER_TOKEN`) are not wrapped.

Errors reject with `HeimdallError` (`status`, `type`, `serverMessage`, `mode`); `HeimdallTimeout` covers `timeoutMs` (default 30000) and `waitForExperiment`. Reads retry `readRetries` times (default 2) on network errors, 502 and 504; writes never retry.

Methods mirror the Python client in camelCase: `health`, `doctor`, `catalog`, `openapi`, `suites`, `suiteTask`, `agents`, `experiments`, `experiment`, `plan`, `createExperiment`, `startExperiment`, `runExperiment`, `events`, `grid`, `verdict`, `analysis`/`diagnostics`, `triage`, `runs`, `run`, `runEvents`, `runDiff`, `runPatch`, `runReceipts`, `runFile`, `pair`, `leaderboard`, `audit`, `releases`, `release`, `createRelease`, `releaseEvidence`, `imports`, `workers`, `waitForExperiment`, `check`.

```sh
npm run typecheck        # tsc --noEmit (bench/ui's tsc works: ../../ui/node_modules/.bin/tsc -p tsconfig.json)
npm test                 # node --experimental-strip-types --test (Node 22.6+); live tests skip without HEIMDALL_URL
npm run build            # emits dist/ for publishing
```

CI gate example: `bench/clients/examples/ci-gate.ts`. Guide: `bench/docs/clients.md`.

## Reporting agent events

`emit`, `step` and `stdoutLine` let an agent under test report its own internal steps (plans, tool calls and results, subagents, handoffs, memory and retrieval) in the format of `bench/spec/agent-event.schema.json`. The bench shows them as agent-reported evidence beside what it observed itself; they never raise a trusted metric or decide an outcome.

```ts
import { emit, flushEvents, step } from "@heimdall/bench-client";

const plan = emit("plan", "fix the failing test", { output: "run the tests, read the failure, patch" });
const call = emit("tool_call", "run_shell", { parentId: plan, toolCallId: "call_1", input: { command: "npm test" } });
emit("tool_result", "run_shell", { parentId: call, toolCallId: "call_1", output: "1 failed", status: "error" });
await step("subagent_start", "reviewer", async (sub) => { sub.output = "the fixture is stale"; }, { parentId: plan });
await flushEvents(); // delivery runs in the background: wait before the process exits
```

Delivery follows the variables the bench sets in the agent container: `BENCH_EVENTS_URL` (a POST of JSON lines, 2 second timeout), else `BENCH_EVENTS_FILE` (one JSON object appended per line with `node:fs`). A failed POST falls back to the file when both are set, and the URL is then skipped for 30 seconds. With neither set every call is a no-op that still returns an id, and nothing ever throws into the agent. `emit` returns the event id (32 hex characters unless you pass `id`); nest events by passing it as `parentId`. `step` times a function and reports it when it settles (status `error`, rethrown, when it throws). `input` and `output` take any JSON value and are cut to 8 KiB of text. `stdoutLine(...)` returns the `@@heimdall-event ` line for an agent that can only print. Full example: `bench/clients/examples/agent-events.ts`.
