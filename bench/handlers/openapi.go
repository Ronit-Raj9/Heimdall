package handlers

import (
	"cmp"
	"encoding/json"
	"fmt"
	"github.com/maximhq/bifrost/bench/openmodel"
	"github.com/maximhq/bifrost/bench/sandbox"
	"path"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/canary" // W5 drift canary
	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/costexplain" // W6 cost explanation
	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/improve" // improvement runs
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livescore" // X6 live scoring
	"github.com/maximhq/bifrost/bench/migration" // W6 migration report
	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// OpenAPIPath serves the machine-readable description of the bench API.
const OpenAPIPath = APIPrefix + "/openapi.json"

// Route auth: who may call a route.
const (
	AuthNone   = ""       // anyone the host guard lets in
	AuthWrite  = "write"  // Authorization: Bearer <BENCH_WRITE_TOKEN>
	AuthWorker = "worker" // Authorization: Bearer <BENCH_WORKER_TOKEN>
	AuthInbox  = "inbox"  // Failure inbox area: Authorization: Bearer <the webhook source's token_env>
)

// RouteDoc describes one route of the bench API for the OpenAPI description.
// Request and Response hold a zero value of the JSON body type; the schema is
// derived from it by reflection, so it cannot drift from the Go type.
type RouteDoc struct {
	Method       string
	Path         string // router pattern, e.g. /api/bench/runs/{id}
	Tag          string
	Summary      string
	Auth         string
	Query        []QueryDoc
	Request      any      // JSON request body, nil when there is none
	RequestAlso  []string // other request media types the route accepts, e.g. application/yaml
	RawRequest   string   // media type of a non-JSON request body
	Status       int      // success status; 0 means 200
	Response     any      // JSON success body; the server adds "mode"
	Raw          string   // media type of a non-JSON success body
	AlsoRaw      string   // a second media type the route can answer with, e.g. text/event-stream
	NoContent    bool     // the route may also answer 204 with no body
	OptionalBody bool     // the request body may be omitted; the route then uses defaults
}

// QueryDoc is one query parameter.
type QueryDoc struct {
	Name        string
	Description string
}

// APIRoutes is the description table: one entry per route the server
// registers. TestOpenAPICoversEveryRoute in openapi_test.go fails, listing
// the routes, when the server registers a route this table lacks.
var APIRoutes = []RouteDoc{
	{Method: "GET", Path: APIPrefix + "/health", Tag: "health", Summary: "Liveness and server version", Response: schemas.Health{}},
	{Method: "GET", Path: APIPrefix + "/doctor", Tag: "health", Summary: "Preflight checks", Response: schemas.Doctor{}},
	{Method: "GET", Path: APIPrefix + "/catalog", Tag: "catalog", Summary: "Every registered suite, probe, agent and metric", Response: schemas.Catalog{}},
	{Method: "GET", Path: OpenAPIPath, Tag: "meta", Summary: "This OpenAPI 3.1 description", Raw: "application/json"},

	{Method: "GET", Path: APIPrefix + "/experiments", Tag: "experiments", Summary: "List experiments with their status", Response: schemas.ExperimentList{}},
	{Method: "POST", Path: APIPrefix + "/experiments", Tag: "experiments", Summary: "Plan, freeze and create an experiment; nothing runs until start",
		Auth: AuthWrite, Request: schemas.Experiment{}, RequestAlso: experimentMediaTypes, Status: fasthttp.StatusCreated, Response: schemas.ExperimentCreated{}},
	{Method: "POST", Path: APIPrefix + "/experiments/plan", Tag: "experiments", Summary: "Size an experiment without running anything; planner errors come back in errors with 200",
		Auth: AuthWrite, Request: schemas.Experiment{}, RequestAlso: experimentMediaTypes, Response: schemas.ExperimentPlan{}},
	{Method: "POST", Path: APIPrefix + "/experiments/{id}/start", Tag: "experiments", Summary: "Start a created experiment",
		Auth: AuthWrite, Status: fasthttp.StatusAccepted, Response: schemas.ExperimentStatus{}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}", Tag: "experiments", Summary: "Experiment status", Response: schemas.ExperimentStatus{}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/verdict", Tag: "analysis", Summary: "Release verdict, gate checklist, config diff and policy result",
		Query: []QueryDoc{gradeSourceQuery}, Response: schemas.VerdictPayload{}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/diagnostics", Tag: "analysis", Summary: "Diagnostics table, meters and per-arm metrics",
		Query: []QueryDoc{gradeSourceQuery}, Response: schemas.DiagnosticsPayload{}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/grid", Tag: "experiments", Summary: "Task by trial grid of slots and runs", Response: schemas.Grid{}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/events", Tag: "experiments", Summary: "Live events: server-sent events for Accept text/event-stream, else a poll",
		Query: []QueryDoc{{Name: "since", Description: "Return events after this sequence number"}}, Response: schemas.EventsPoll{}, AlsoRaw: "text/event-stream"},
	// Jobs area (P4, handlers/experimentjobs.go): experiments run as experiment.run jobs.
	{Method: "POST", Path: APIPrefix + "/experiments/{id}/cancel", Tag: "experiments", Summary: "Cancel a running experiment: no new slot is leased, in-flight runs stop and seal as aborted_infra (never counted); the state becomes cancelled; 409 when nothing runs",
		Auth: AuthWrite, Status: fasthttp.StatusAccepted, Response: schemas.ExperimentStatus{}},
	{Method: "POST", Path: APIPrefix + "/experiments/{id}/resume", Tag: "experiments", Summary: "Queue a stopped experiment's run again; sealed runs are kept and the rest run; 409 while a run or regrade job is active",
		Auth: AuthWrite, Status: fasthttp.StatusAccepted, Response: schemas.ExperimentStatus{}},
	{Method: "POST", Path: APIPrefix + "/experiments/{id}/regrade", Tag: "analysis", Summary: "Queue an experiment.regate job: recompute the gates of the sealed runs as a new regrade (id in the job's params); nothing runs again",
		Auth: AuthWrite, Request: RegradeRequest{}, OptionalBody: true, Status: fasthttp.StatusAccepted, Response: jobs.Job{}},
	// Run control (handlers/runcontrol.go): pause (drain), live controls with a hash-chained log, run cancel and requeue, rerun failures.
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/controls", Tag: "experiments", Summary: "Run controls: paused or running, live concurrency, spend cap and paused models, the error policy table, the verified change log and whether Rerun failures can work (rerun_failures, with the reason when not)",
		Response: RunControls{}},
	{Method: "PATCH", Path: APIPrefix + "/experiments/{id}/controls", Tag: "experiments", Summary: "Change live concurrency, spend cap or paused models of an experiment; the scheduler applies it from the next lease; logged in controls.jsonl",
		Auth: AuthWrite, Request: runctl.Patch{}, Response: RunControls{}},
	{Method: "POST", Path: APIPrefix + "/experiments/{id}/pause", Tag: "experiments", Summary: "Pause a running experiment: in-flight runs finish, no new run starts until resume; 409 when it is not running",
		Auth: AuthWrite, Request: RunControlRequest{}, OptionalBody: true, Status: fasthttp.StatusAccepted, Response: RunControls{}},
	{Method: "POST", Path: APIPrefix + "/experiments/{id}/rerun-failures", Tag: "experiments", Summary: "Create and start an exploratory experiment that runs the failed tasks again; it never joins this experiment's verdict or a leaderboard",
		Auth: AuthWrite, Request: RerunFailuresRequest{}, OptionalBody: true, Status: fasthttp.StatusAccepted, Response: RerunFailuresResult{}},
	{Method: "POST", Path: APIPrefix + "/experiments/{id}/runs/{run}/cancel", Tag: "experiments", Summary: "Cancel one in-flight run: sealed aborted_infra, never counted, no attempt used; its slot waits for a requeue",
		Auth: AuthWrite, Request: RunControlRequest{}, OptionalBody: true, Status: fasthttp.StatusAccepted, Response: RunControls{}},
	{Method: "POST", Path: APIPrefix + "/experiments/{id}/runs/{run}/requeue", Tag: "experiments", Summary: "Run a slot again: only its newest run, only when aborted_infra, infra_error or cancelled; the old run is kept, marked superseded, and uses no attempt",
		Auth: AuthWrite, Request: RunControlRequest{}, OptionalBody: true, Status: fasthttp.StatusAccepted, Response: RunControls{}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/triage", Tag: "analysis", Summary: "Runs grouped by failure pattern per arm", Response: schemas.Triage{}},
	{Method: "POST", Path: APIPrefix + "/estimate", Tag: "experiments", Summary: "Forecast an experiment's cost and time from sealed history; nothing is frozen, a refused plan comes back with fit unknown and 200",
		Auth: AuthWrite, Request: schemas.Experiment{}, RequestAlso: experimentMediaTypes, Response: schemas.Forecast{}}, // Forecast and matrix area (forecast.go)
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/matrix", Tag: "analysis", Summary: "Every arm compared: success, pass^k, cost per success, pairwise sign-flip p values with Holm", Response: schemas.ModelMatrix{}, Query: []QueryDoc{
		{Name: "condition", Description: "Condition; default the experiment's primary condition"}, gradeSourceQuery,
		{Name: "k", Description: "pass^k depth, 2 or more"}, {Name: "alpha", Description: "Significance level; default 0.05"},
	}}, // Forecast and matrix area (forecast.go)
	// W6 migration report and cost explanation (migration.go, costexplain.go, docs/model-migration.md, docs/cost-explain.md).
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/migration", Tag: "analysis", Summary: "Model migration report: verdict per model pair overall and per stratum (exact sign-flip, Holm), cost per verified success and why it moved, behaviour differences, flipped tasks, a recommendation that follows the release policy", Response: migration.Report{}, Query: []QueryDoc{
		{Name: "from", Description: "Old model or arm id; default arm A"}, {Name: "to", Description: "New models or arm ids, comma-separated; default every other arm of the same agent"},
		{Name: "condition", Description: "Condition of the overall verdict; default the experiment's primary condition"}, {Name: "alpha", Description: "Significance level; default 0.05"}, gradeSourceQuery,
	}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/cost-explain", Tag: "analysis", Summary: "Why arm B cost more than arm A: additive parts of the difference in cost per run and per verified success, measuring costs apart, silent cache breaks with their likely cause", Response: costexplain.Explanation{}, Query: []QueryDoc{
		{Name: "a", Description: "Baseline arm; default the first arm"}, {Name: "b", Description: "Arm to explain; default the second arm"},
		{Name: "condition", Description: "Condition; default the experiment's primary condition"}, gradeSourceQuery,
	}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/pareto", Tag: "analysis", Summary: "Every arm on the cost against verified success plane, the frontier marked", Response: costexplain.Pareto{}, Query: []QueryDoc{
		{Name: "x", Description: "cost_per_success (default) or cost_per_run"}, {Name: "condition", Description: "Condition; default the experiment's primary condition"},
	}},
	{Method: "GET", Path: APIPrefix + "/leaderboard/agents", Tag: "leaderboard", Summary: "One row per agent, version and model with a score per capability over each benchmark's newest version, clean runs", Response: AgentLeaderboard{}, Query: []QueryDoc{
		{Name: "capability", Description: "Only rows with a score for this capability, ordered by it"}, {Name: "model", Description: "Only rows on this model"},
	}},
	{Method: "GET", Path: APIPrefix + "/leaderboard/pareto", Tag: "leaderboard", Summary: "The leaderboard's configs on the cost against verified success plane, the frontier marked", Response: costexplain.Pareto{}, Query: []QueryDoc{
		{Name: "x", Description: "cost_per_success (default) or cost_per_run (median)"}, {Name: "suite", Description: "Suite id or id@version, as for GET /leaderboard"},
		{Name: "condition", Description: "Condition, as for GET /leaderboard"}, {Name: "scope", Description: "all or common"}, {Name: "synthetic", Description: "exclude or only"},
		gradeSourceQuery, {Name: "split", Description: "public or held_out"}, {Name: "k", Description: "pass^k depth"},
	}},

	// UI regroup (U2): the run list across experiments (handlers/runlist.go).
	{Method: "GET", Path: APIPrefix + "/runs", Tag: "runs", Summary: "Every run across experiments, newest first unless sort says otherwise, with filters, sorting, paging and the values each filter can take", Response: RunList{}, Query: []QueryDoc{
		{Name: "exp", Description: "Experiment id"}, {Name: "suite", Description: "Suite id"}, {Name: "arm", Description: "Arm id"},
		{Name: "task", Description: "Task id"}, {Name: "condition", Description: "clean or a probe id"}, {Name: "outcome", Description: "Run outcome"},
		{Name: "track", Description: "Suite track"}, {Name: "agent", Description: "Agent config id"}, {Name: "benchmark", Description: "Suite id with or without its version"},
		{Name: "fault", Description: "Fault name (model-down, broken-tool, misleading-instruction, ...) or clean"}, {Name: "model", Description: "Arm model"},
		{Name: "sort", Description: "Sort key, applied before paging: started (default), duration, outcome, cost, tokens, steps, task or agent; rows without a value go last"},
		{Name: "dir", Description: "Sort direction: asc or desc (default desc)"},
		{Name: "limit", Description: "Page size; default 100, at most 500"}, {Name: "offset", Description: "Rows to skip"},
	}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}", Tag: "runs", Summary: "One run with seal, grade, metrics, gates and receipts", Response: schemas.RunDetail{}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/events", Tag: "runs", Summary: "Canonical event log of a run", Response: schemas.RunEvents{}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/diff", Tag: "runs", Summary: "Patch the agent produced; ?format=text answers the raw patch",
		Query: []QueryDoc{{Name: "format", Description: "text for the raw patch as text/plain"}}, Response: schemas.RunDiff{}, AlsoRaw: "text/plain"},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/receipts", Tag: "runs", Summary: "Fault receipts of a run", Response: schemas.RunReceipts{}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/files/{name}", Tag: "runs", Summary: "One allowlisted run file, raw; mode in X-Bench-Mode", Raw: "application/octet-stream"},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/evidence/{name}", Tag: "runs", Summary: "Raw audit evidence from a sealed, verified run", Raw: "application/octet-stream"},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/artifacts/{artifact}/{name}", Tag: "runs", Summary: "One file of a run's output artifact, raw and sandboxed", Raw: "application/octet-stream"},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/visual/{name}", Tag: "runs", Summary: "One image of a run's visual grade (capture.png, reference.png or diff.png), checked against the grade and the seal", Raw: "image/png"}, // X1 vision grading
	{Method: "GET", Path: APIPrefix + "/runs/{id}/frames", Tag: "runs", Summary: "Screen replay frames of a computer-use or browser run: index, time, action and image URL"},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/frames/{n}", Tag: "runs", Summary: "One screen replay frame image of a run (png or jpeg)", Raw: "image/png"},

	{Method: "GET", Path: APIPrefix + "/runs/{id}/voice", Tag: "runs", Summary: "The run's voice call: the caller's timeline read as the voice metrics read it, and the kept audio tracks", Response: VoiceCall{}},                       // X8 voice agents
	{Method: "GET", Path: APIPrefix + "/runs/{id}/voice/audio/{track}", Tag: "runs", Summary: "One audio track of a run's voice call (caller or agent), from a sealed run, checked against the evidence and the seal", Raw: "audio/wav"}, // X8 voice agents

	{Method: "GET", Path: APIPrefix + "/runs/{id}/otlp", Tag: "runs", Summary: "A sealed run as an OpenTelemetry trace (OTLP/JSON download); ?payloads=true adds redacted, capped tool payloads",
		Query: []QueryDoc{{Name: "payloads", Description: "true to include tool arguments and results, redacted and capped"}}, Raw: "application/json"},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/trace", Tag: "runs", Summary: "Download one run's trace: step timeline and events as JSON, the steps as JSONL, or the sealed-run OTLP export",
		Query: []QueryDoc{{Name: "format", Description: "json (default), jsonl or otlp"}}, Raw: "application/json"},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/steps", Tag: "runs", Summary: "Unified agent step timeline of a run with its observer status; available false for runs recorded before it", Response: RunSteps{}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/agents", Tag: "runs", Summary: "Agents of a multi-agent run: tree, per-agent cost and lanes, handoffs with payloads, and the blame; available false for a single-agent run", Response: RunAgents{}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/remote", Tag: "runs", Summary: "Remote agent channel summary of a run: what is trusted, agent-reported or unknown; available false for other agents", Response: RunRemote{}},          // Remote coverage
	{Method: "GET", Path: APIPrefix + "/runs/{id}/a2a", Tag: "runs", Summary: "Agent gateway witness of an a2a run: operations, hops, linked LLM and MCP rows, answer agreement; available false for other agents", Response: RunA2A{}}, // B1
	// X4: code scan of added lines.
	{Method: "GET", Path: APIPrefix + "/runs/{id}/codescan", Tag: "runs", Summary: "Security findings on the lines the run's agent added (rule, CWE, severity, file, line, redacted snippet), sealed or computed now from the sealed diff, with the experiment's veto level", Response: schemas.CodeScanReport{}},
	// X2: services of a run and a compose service's captured log.
	{Method: "GET", Path: APIPrefix + "/runs/{id}/services", Tag: "runs", Summary: "Services that ran beside the agent: image, readiness, error, and the size of each captured compose service log", Response: RunServices{}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/services/{service}/log", Tag: "runs", Summary: "One compose service's captured output (stdout and stderr, timestamped), from a sealed run that verifies against its seal; plain text", Raw: "text/plain"},
	{Method: "GET", Path: APIPrefix + "/pairs/{id}", Tag: "runs", Summary: "Both arms of a pair with their aligned steps", Response: schemas.Pair{}},

	{Method: "GET", Path: APIPrefix + "/leaderboard", Tag: "leaderboard", Summary: "Leaderboard over sealed runs", Response: schemas.Leaderboard{}, Query: []QueryDoc{
		{Name: "suite", Description: "Suite id; default the newest suite with runs"},
		{Name: "condition", Description: "Condition; default clean"},
		{Name: "scope", Description: "Which runs count; default all"},
		{Name: "synthetic", Description: "exclude (default) or include synthetic runs"},
		{Name: "grade_source", Description: "original (default) or latest"},
		{Name: "split", Description: "public (default) or held_out: the separate held-out board, served only with held_out.visible (U3)"},
		{Name: "k", Description: "pass@k and pass^k depth, 2 or more"},
	}},
	// Failure clusters area (handlers/clusters.go).
	{Method: "GET", Path: APIPrefix + "/clusters", Tag: "analysis", Summary: "Failed runs across experiments grouped by cause (docs/failure-clusters.md)", Response: clusters.Report{}, Query: ClusterQueryDocs},
	{Method: "GET", Path: APIPrefix + "/clusters/{signature}", Tag: "analysis", Summary: "One failure cluster with its runs", Response: clusters.Detail{}, Query: ClusterQueryDocs},
	// Index and trends area (handlers/trends.go).
	TrendsRouteDoc,
	SearchRouteDoc, // X23 cross-run trace search (search.go)
	// X23 Maxim integration (maxim.go).
	{Method: "GET", Path: APIPrefix + "/integrations/maxim", Tag: "integrations", Summary: "The org's Maxim connection: workspace, log repository, automatic sending, push counts (never the key)", Response: MaximConnection{}},
	{Method: "PUT", Path: APIPrefix + "/integrations/maxim", Tag: "integrations", Summary: "Save the Maxim connection; an empty api_key keeps the stored key", Auth: AuthWrite, Request: MaximConnectionPut{}, Response: MaximConnection{}},
	{Method: "DELETE", Path: APIPrefix + "/integrations/maxim", Tag: "integrations", Summary: "Disconnect Maxim and delete the stored key", Auth: AuthWrite, Response: MaximConnection{}},
	{Method: "POST", Path: APIPrefix + "/integrations/maxim/test", Tag: "integrations", Summary: "Check the stored key, workspace and log repository with Maxim", Auth: AuthWrite, Response: MaximTestResult{}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/maxim", Tag: "runs", Summary: "A run's Maxim push status and its Open in Maxim link", Response: RunMaxim{}},
	{Method: "POST", Path: APIPrefix + "/runs/{id}/maxim/push", Tag: "runs", Summary: "Send a sealed run's trace to the org's Maxim log repository now", Auth: AuthWrite, Response: RunMaxim{}},
	{Method: "POST", Path: APIPrefix + "/audit", Tag: "analysis", Summary: "Verdict for a published per-task table (JSON rows or CSV)",
		Auth: AuthWrite, Request: schemas.AuditRequest{}, RequestAlso: []string{"text/csv"}, Response: schemas.Verdict{}},

	{Method: "POST", Path: APIPrefix + "/releases", Tag: "releases", Summary: "Record a release decision in the hash-chained log",
		Auth: AuthWrite, Request: schemas.ReleaseRequest{}, Status: fasthttp.StatusCreated, Response: schemas.ReleaseRecord{}},
	{Method: "GET", Path: APIPrefix + "/releases", Tag: "releases", Summary: "Release log", Response: schemas.ReleaseList{}},
	{Method: "GET", Path: APIPrefix + "/releases/{id}", Tag: "releases", Summary: "One release record", Response: schemas.ReleaseRecord{}},
	{Method: "GET", Path: APIPrefix + "/releases/{id}/evidence", Tag: "releases", Summary: "Evidence pack of a release (zip)", Raw: "application/zip"},

	// Human review area (reviews.go, docs/human-review.md).
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/reviews/queue", Tag: "reviews", Summary: "Runs that need a human review: split or unparsable judge votes, undecided grader, fired veto, false completion, flagged",
		Query: []QueryDoc{{Name: "reason", Description: "Keep only runs with this queue reason"}}, Response: schemas.ReviewQueue{}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/reviews", Tag: "reviews", Summary: "The experiment's hash-chained review log and whether it verifies", Response: schemas.ReviewLog{}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/reviews", Tag: "reviews", Summary: "Every review of a run, oldest first, and the one in effect", Response: schemas.RunReviews{}},
	{Method: "POST", Path: APIPrefix + "/reviews", Tag: "reviews", Summary: "Record a human review of a sealed run; refused (403) outside live mode; the reviewer name is self-declared",
		Auth: AuthWrite, Request: schemas.ReviewRequest{}, Status: fasthttp.StatusCreated, Response: schemas.Review{}},

	// W9 routing policy under test (routing.go, docs/routing-experiments.md).
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/routing", Tag: "analysis", Summary: "Gateway policy card: every arm's routing policy, how the arms' policies differ, and per condition fallbacks fired, model switches and turns, cost and latency by target", Response: schemas.RoutingSummary{}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/support", Tag: "analysis", Summary: "Support scorecard (W3): per arm resolution (verified success), cost per resolution, escalation and policy violation rates, user turns and simulator cost, with the simulated user's label; ?condition= picks the condition (default clean)", Response: schemas.SupportSummary{}},
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/research", Tag: "analysis", Summary: "Research scorecard (W4): per arm verified share, mean searches, sources cited, citations grounded in the run's own Tavily results and citation precision, and replayed searches the cassette did not hold; replay false for a live or record experiment; ?condition= picks the condition (default clean)", Response: schemas.ResearchSummary{}}, // W4 research with Tavily
	// W7 adaptive trials and suite tiers (adaptive.go, docs/adaptive-trials.md).
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/adaptive", Tag: "analysis", Summary: "Trials and tier panel (W7): the suite tier, the declared trials policy, the frozen adaptive design, trials used against the maximum, runs saved, the stopping reason, and what more evidence would cost", Response: AdaptiveView{}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/routing", Tag: "runs", Summary: "One run's gateway policy, the Bifrost rows that show which target served each request, and the model fidelity result", Response: schemas.RunRouting{}},
	{Method: "GET", Path: APIPrefix + "/runs/{id}/bifrost", Tag: "runs", Summary: "Through Bifrost (C09): one run's model calls as Bifrost saw them, from the Bifrost log rows, the fault ledger and the heimdall plugin usage records when present: providers and models served, fallbacks, cache hits, faults applied, tokens, cost and latency per call", Response: RunBifrost{}},

	// W10 export (export.go, docs/export.md).
	{Method: "GET", Path: APIPrefix + "/experiments/{id}/export", Tag: "analysis", Summary: "Download the experiment's sealed results: format=eee (Every Eval Ever aggregate records, one JSON object per line, one per arm; synthetic results refused (409) unless allow_synthetic=true, then labelled), jsonl or csv (one row per sealed run)",
		Query: []QueryDoc{{Name: "format", Description: "eee, jsonl or csv"}, {Name: "allow_synthetic", Description: "true exports synthetic arms, labelled (eee)"}}, Raw: "application/x-ndjson", AlsoRaw: "text/csv"},
	{Method: "GET", Path: APIPrefix + "/suites/{id}/export", Tag: "suites", Summary: "Download the suite's admitted public tasks as Harbor task folders (gzip tar); every verifier and reference solution is withheld, never served",
		Query: []QueryDoc{{Name: "format", Description: "harbor"}}, Raw: "application/gzip"},

	// X6 live scoring (livescore.go, docs/live-scoring.md). Live evidence: no ground truth, never part of a verdict.
	{Method: "GET", Path: APIPrefix + "/livescore", Tag: "livescore", Summary: "Live scoring: the gateway read (read-only), every version's signals over time, session grouping reliability, the comparison and the worst sessions with their inbox items", Response: livescore.Status{}},
	{Method: "GET", Path: APIPrefix + "/livescore/sessions", Tag: "livescore", Summary: "Scored live sessions, newest first: numbers and flags only, never a prompt or an output",
		Query: []QueryDoc{{Name: "version", Description: "a configured version"}, {Name: "flag", Description: "abandoned, tool_error, loop, injection_marker, budget_hit, refusal, ..."}, {Name: "limit", Description: "1 to 1000, default 200"}}, Response: livescore.SessionList{}},
	{Method: "GET", Path: APIPrefix + "/livescore/compare", Tag: "livescore", Summary: "The running comparison of two live versions: better, worse or not yet known per signal, with pairs, means and e-values; 404 until one has started", Response: livescore.Comparison{}},
	{Method: "POST", Path: APIPrefix + "/livescore/pull", Tag: "livescore", Summary: "Pull the gateway's logs once now (GET requests only towards the gateway); live mode only",
		Auth: AuthWrite, Response: livescore.PullResult{}},

	// Failure inbox area (inbox.go, docs/failure-inbox.md).
	{Method: "GET", Path: APIPrefix + "/inbox", Tag: "inbox", Summary: "Failure inbox: production failures pulled from observability tools, with counts by state, sources and suites",
		Query: []QueryDoc{{Name: "state", Description: "new, accepted, rejected, duplicate or snoozed"}, {Name: "source", Description: "Keep only items from this source"}}, Response: schemas.InboxList{}},
	{Method: "GET", Path: APIPrefix + "/inbox/{id}", Tag: "inbox", Summary: "One inbox item with its redacted transcript preview", Response: schemas.InboxItemDetail{}},
	{Method: "POST", Path: APIPrefix + "/inbox/{id}/accept", Tag: "inbox", Summary: "Turn an item into a task: a draft with EDIT ME markers, or with a guide (what should have happened) a finished task that only needs its readiness check",
		Auth: AuthWrite, Request: schemas.InboxAcceptRequest{}, Response: schemas.InboxAcceptResult{}},
	{Method: "POST", Path: APIPrefix + "/inbox/{id}/reject", Tag: "inbox", Summary: "Reject an item with a reason, or mark it a duplicate of another",
		Auth: AuthWrite, Request: schemas.InboxRejectRequest{}, Response: schemas.InboxActionResult{}},
	{Method: "POST", Path: APIPrefix + "/inbox/{id}/snooze", Tag: "inbox", Summary: "Hide an item for some days",
		Auth: AuthWrite, Request: schemas.InboxSnoozeRequest{}, Response: schemas.InboxActionResult{}},
	{Method: "POST", Path: APIPrefix + "/inbox/ingest", Tag: "inbox", Summary: "Push one flagged trace (OTLP, Langfuse or chat JSON); the webhook's own token, a size cap and a rate limit guard it; 503 while off",
		Auth: AuthInbox, Request: traceinbox.IngestRequest{}, Status: fasthttp.StatusCreated, Response: schemas.InboxIngestResult{}},

	{Method: "GET", Path: APIPrefix + "/suites", Tag: "suites", Summary: "Suites with their tasks", Response: schemas.SuiteList{},
		Query: []QueryDoc{{Name: "fields", Description: "light: no per-task lists, never waits for a load (pickers); full: the default"}}},
	{Method: "GET", Path: APIPrefix + "/suites/{id}/tasks/{task}", Tag: "suites", Summary: "One task of a suite", Response: schemas.SuiteTaskDetail{}},
	{Method: "GET", Path: APIPrefix + "/suites/{id}/health", Tag: "suites", Summary: "Task health of a suite: verifier flakiness, agent variance, infra, observed difficulty", Response: schemas.SuiteHealth{}}, // Suite health area (suitehealth.go)
	// W8 judge calibration (calibration.go, docs/judge-calibration.md).
	{Method: "GET", Path: APIPrefix + "/suites/{id}/calibration", Tag: "suites", Summary: "Judge calibration card of a suite: state, agreement, kappa, false-positive bound, label counts, newest record, drift reminders", Response: schemas.CalibrationStatus{}},
	{Method: "GET", Path: APIPrefix + "/suites/{id}/calibration/labels", Tag: "suites", Summary: "Every row of the suite's calibration labels, oldest first", Response: schemas.CalibrationLabels{}},
	{Method: "POST", Path: APIPrefix + "/suites/{id}/calibration/labels", Tag: "suites", Summary: "Append a human label (correct or incorrect) for an answer to a task; refused (403) outside live mode; the labeller name is self-declared",
		Auth: AuthWrite, Request: schemas.CalibrationLabelRequest{}, Status: fasthttp.StatusCreated, Response: schemas.CalibrationItem{}},
	{Method: "GET", Path: APIPrefix + "/agents", Tag: "agents", Summary: "Agent configs", Response: schemas.AgentList{}},
	AgentDiffRouteDoc, AgentDiffVersionsRouteDoc, // Agent diff area (agentdiff.go)
	ComplianceRouteDoc, // Eval integrity area (S2): compliance pack (compliance.go)
	AwarenessRouteDoc,  // Eval integrity area (S2): eval awareness scan (awareness.go)
	// Eval integrity area (S2): monitor certification (monitorcert.go).
	MonitorCertListRouteDoc, MonitorCertGetRouteDoc, MonitorCertReportRouteDoc,
	{Method: "GET", Path: APIPrefix + "/imports", Tag: "imports", Summary: "Importers the binary links", Response: ImportList{Importers: []importers.Info{}}},

	{Method: "GET", Path: APIPrefix + "/workers", Tag: "workers", Summary: "Remote workers; enabled is false when worker mode is off", Response: WorkersList{}},
	{Method: "POST", Path: APIPrefix + "/workers/register", Tag: "workers", Summary: "Register a remote worker",
		Auth: AuthWorker, Request: schemas.WorkerRegister{}, Response: schemas.WorkerRegistered{}},
	{Method: "POST", Path: APIPrefix + "/workers/lease", Tag: "workers", Summary: "Lease the next slot; 204 when there is none",
		Auth: AuthWorker, Request: schemas.WorkerLeaseRequest{}, Response: schemas.WorkerLease{}, NoContent: true},
	{Method: "GET", Path: APIPrefix + "/workers/manifests/{exp_id}", Tag: "workers", Summary: "Frozen manifest of a leased experiment",
		Auth: AuthWorker, Query: []QueryDoc{workerIDQuery}, Response: schemas.Manifest{}},
	{Method: "POST", Path: APIPrefix + "/workers/leases/{lease_id}/heartbeat", Tag: "workers", Summary: "Keep a lease alive",
		Auth: AuthWorker, Request: schemas.WorkerHeartbeat{}, Status: fasthttp.StatusNoContent},
	{Method: "POST", Path: APIPrefix + "/workers/leases/{lease_id}/upload", Tag: "workers", Summary: "Append a chunk of the run bundle at offset",
		Auth: AuthWorker, RawRequest: "application/octet-stream", Response: schemas.WorkerUploadState{},
		Query: []QueryDoc{workerIDQuery, {Name: "offset", Description: "Byte offset of this chunk"}}},
	{Method: "POST", Path: APIPrefix + "/workers/leases/{lease_id}/complete", Tag: "workers", Summary: "Finish a lease and ingest the uploaded run",
		Auth: AuthWorker, Request: schemas.WorkerComplete{}, Response: schemas.WorkerResult{}},
	{Method: "POST", Path: APIPrefix + "/workers/leases/{lease_id}/release", Tag: "workers", Summary: "Give a lease back unfinished",
		Auth: AuthWorker, Request: schemas.WorkerRelease{}, Status: fasthttp.StatusNoContent},
	// Runners area (P5, handlers/workers.go, handlers/runners.go, docs/runners.md). Hosted mode: the worker routes take a
	// runner token (hmr_...) scoped to one org and pool; 403 for another org's or pool's work. The runner admin routes need
	// an admin of the current org and answer 503 in local mode.
	{Method: "GET", Path: APIPrefix + "/workers/whoami", Tag: "workers", Summary: "What the caller's token is scoped to and the runners registered with it",
		Auth: AuthWorker, Response: schemas.RunnerWhoami{}},
	{Method: "GET", Path: APIPrefix + "/runners", Tag: "runners", Summary: "Registered runners of the org: state, pool, version, last seen, running slots",
		Auth: AuthWrite, Response: schemas.RunnersList{}},
	{Method: "GET", Path: APIPrefix + "/runners/{id}", Tag: "runners", Summary: "One runner of the org", Auth: AuthWrite, Response: schemas.RunnerView{}},
	{Method: "POST", Path: APIPrefix + "/runners/{id}/remove", Tag: "runners", Summary: "Remove a runner: its leases are lost now; revoke its token to keep it out",
		Auth: AuthWrite, Response: schemas.RunnerView{}},
	{Method: "GET", Path: APIPrefix + "/runners/tokens", Tag: "runners", Summary: "Runner tokens of the org, without secrets", Auth: AuthWrite, Response: schemas.RunnerTokensList{}},
	{Method: "POST", Path: APIPrefix + "/runners/tokens", Tag: "runners", Summary: "Create a runner token for one pool; the token and the runner start command are shown once",
		Auth: AuthWrite, Request: schemas.RunnerTokenCreate{}, Status: fasthttp.StatusCreated, Response: schemas.RunnerTokenCreated{}},
	{Method: "POST", Path: APIPrefix + "/runners/tokens/{id}/revoke", Tag: "runners", Summary: "Revoke a runner token now; its runners lose their leases",
		Auth: AuthWrite, Response: schemas.RunnerTokenView{}},
	{Method: "GET", Path: APIPrefix + "/runners/install.sh", Tag: "runners", Summary: "Runner install script for a fresh Ubuntu or Debian box (deploy/runner/install.sh)",
		Raw: "text/x-shellscript"},

	// Bring your own worker (X19a, handlers/workerplatforms.go): the org's own cloud sandbox platforms.
	{Method: "GET", Path: APIPrefix + "/worker-platforms", Tag: "runners", Summary: "Cloud sandbox platforms (Modal, Daytona, E2B), which are connected and the org's default worker; tokens are never returned",
		Auth: AuthWrite, Response: sandbox.ConnectionsList{}},
	{Method: "POST", Path: APIPrefix + "/worker-platforms/default", Tag: "runners", Summary: "Alias of PUT /workers/target: platform:<name>, or the shared worker for \"\" (clears an own worker default)",
		Auth: AuthWrite, Request: PlatformDefaultRequest{}, Response: sandbox.ConnectionsList{}},
	{Method: "POST", Path: APIPrefix + "/worker-platforms/{platform}/connect", Tag: "runners", Summary: "Test a platform token and keep it as an org secret when the platform accepts it",
		Auth: AuthWrite, Request: PlatformConnectRequest{}, Response: sandbox.ConnectionView{}},
	{Method: "POST", Path: APIPrefix + "/worker-platforms/{platform}/test", Tag: "runners", Summary: "Test a connected platform's token again",
		Auth: AuthWrite, Response: sandbox.ConnectionView{}},
	{Method: "POST", Path: APIPrefix + "/worker-platforms/{platform}/disconnect", Tag: "runners", Summary: "Delete a platform's token; a default platform goes back to the shared worker",
		Auth: AuthWrite, Response: sandbox.ConnectionsList{}},

	// Open models on the org's Modal account (Z05, handlers/openmodels.go, package openmodel).
	{Method: "GET", Path: APIPrefix + "/open-models/catalog", Tag: "models", Summary: "Open models Heimdall can serve on your Modal account (Qwen, DeepSeek, Llama), the engines (vLLM, SGLang) and each GPU with its count and hourly price",
		Response: openmodel.CatalogView{}},
	{Method: "GET", Path: APIPrefix + "/open-models", Tag: "models", Summary: "The org's open model deployments, newest first: state, endpoint, provider model name, GPU and cost limit",
		Response: OpenModelsList{}},
	{Method: "POST", Path: APIPrefix + "/open-models", Tag: "models", Summary: "Serve an open model on the org's Modal account with scale to zero; it becomes the org's vllm or sgl provider key once it answers, and stops when its evaluation ends or its time limit passes",
		Auth: AuthWrite, Request: openmodel.StartRequest{}, Status: fasthttp.StatusAccepted, Response: openmodel.Deployment{}},
	{Method: "GET", Path: APIPrefix + "/open-models/modal-apps", Tag: "models", Summary: "Serving apps on the org's Modal account that no running deployment owns (to stop by hand)",
		Auth: AuthWrite, Response: OpenModelApps{}},
	{Method: "GET", Path: APIPrefix + "/open-models/{id}", Tag: "models", Summary: "One open model deployment",
		Response: openmodel.Deployment{}},
	{Method: "POST", Path: APIPrefix + "/open-models/{id}/stop", Tag: "models", Summary: "Stop the Modal app and delete its provider key now",
		Auth: AuthWrite, Response: openmodel.Deployment{}},

	// Bring your own worker (handlers/workerconns.go, package byoworker): workers an org connects on Runloop, Fly Machines, its Kubernetes cluster or its own VM.
	{Method: "GET", Path: APIPrefix + "/workers/connections", Tag: "workers", Summary: "Connected workers with health, capacity, platform and last seen; the default worker", Response: schemas.WorkerConnectionsList{}},
	{Method: "POST", Path: APIPrefix + "/workers/connections", Tag: "workers", Summary: "Connect a worker; a VM's answer carries its one-line install command (shown once)", Auth: AuthWrite, Request: schemas.WorkerConnectionCreate{}, Status: fasthttp.StatusCreated, Response: schemas.WorkerConnectionCreated{}},
	{Method: "GET", Path: APIPrefix + "/workers/connections/{id}", Tag: "workers", Summary: "One connected worker", Response: schemas.WorkerConnection{}},
	{Method: "POST", Path: APIPrefix + "/workers/connections/{id}/check", Tag: "workers", Summary: "Check the worker's platform credential and refresh its state", Auth: AuthWrite, Response: schemas.WorkerConnection{}},
	{Method: "POST", Path: APIPrefix + "/workers/connections/{id}/start", Tag: "workers", Summary: "Start the worker on its platform", Auth: AuthWrite, Response: schemas.WorkerConnection{}},
	{Method: "POST", Path: APIPrefix + "/workers/connections/{id}/stop", Tag: "workers", Summary: "Stop and remove the worker's box on its platform", Auth: AuthWrite, Response: schemas.WorkerConnection{}},
	{Method: "POST", Path: APIPrefix + "/workers/connections/{id}/remove", Tag: "workers", Summary: "Disconnect the worker: stop it, revoke its token, delete its stored credential", Auth: AuthWrite, Response: schemas.WorkerConnection{}},
	{Method: "POST", Path: APIPrefix + "/workers/connections/{id}/install-command", Tag: "workers", Summary: "Show a VM worker's one-line install command again", Auth: AuthWrite, Response: schemas.WorkerInstallCommand{}},
	{Method: "GET", Path: APIPrefix + "/workers/target", Tag: "workers", Summary: "Where runs go: shared, platform:<modal|daytona|e2b> (a fresh sandbox per run) or worker:<name> (one of the org's own workers), with every choice", Response: schemas.WorkerTargetView{}},
	{Method: "PUT", Path: APIPrefix + "/workers/target", Tag: "workers", Summary: "Set where runs go; the platform and own worker defaults below are its two halves and are kept in step", Auth: AuthWrite, Request: schemas.WorkerTargetSet{}, Response: schemas.WorkerTargetView{}},
	{Method: "PUT", Path: APIPrefix + "/workers/default", Tag: "workers", Summary: "Alias of PUT /workers/target: worker:<name>, or the shared worker when empty (clears a sandbox platform default)", Auth: AuthWrite, Request: schemas.WorkerDefault{}, Response: schemas.WorkerDefault{}},
	{Method: "GET", Path: APIPrefix + "/workers/kubernetes-manifest", Tag: "workers", Summary: "RBAC to apply before connecting a Kubernetes cluster (?namespace=)", Raw: "application/yaml"},

	// Jobs area (P4, handlers/jobs.go, docs/platform-design.md): background jobs of the caller's org.
	{Method: "GET", Path: APIPrefix + "/jobs", Tag: "jobs", Summary: "Jobs of the org, newest first, and the kinds POST /jobs accepts", Response: JobList{}, Query: []QueryDoc{
		{Name: "kind", Description: "Only this kind"}, {Name: "status", Description: "queued, running, succeeded, failed or cancelled"},
		{Name: "target", Description: "Only jobs on this target, e.g. exp:<id> or suite:<id>"}, {Name: "limit", Description: "1..500, default 50"},
		{Name: "before", Description: "Page: jobs created before this job id (next of the previous page)"}}},
	{Method: "POST", Path: APIPrefix + "/jobs", Tag: "jobs", Summary: "Queue a job: suite.lint, suite.lock, suite.admit, agent.check, index.rebuild or experiment.regate, params validated per kind, paths inside the org's project folder (member)",
		Auth: AuthWrite, Request: JobCreate{}, Status: fasthttp.StatusAccepted, Response: jobs.Job{}},
	{Method: "GET", Path: APIPrefix + "/jobs/{id}", Tag: "jobs", Summary: "One job: status, params, progress, result, error, attempt", Response: jobs.Job{}},
	{Method: "GET", Path: APIPrefix + "/jobs/{id}/logs", Tag: "jobs", Summary: "Log lines after since: JSON, or server-sent events (log events, then end) for Accept text/event-stream; the first and newest lines are kept, dropped counts the rest",
		Query:    []QueryDoc{{Name: "since", Description: "Return lines after this sequence number"}, {Name: "limit", Description: "1..2000, default 500"}},
		Response: jobs.LogPage{}, AlsoRaw: "text/event-stream"},
	{Method: "POST", Path: APIPrefix + "/jobs/{id}/cancel", Tag: "jobs", Summary: "Cancel a job: a queued one at once, a running one when its handler stops; 409 when it already ended (member)",
		Auth: AuthWrite, Status: fasthttp.StatusAccepted, Response: jobs.Job{}},
	{Method: "POST", Path: APIPrefix + "/jobs/{id}/priority", Tag: "jobs", Summary: "Move a queued job up or down the claim order (priority -10 to 10, 0 normal); the claim is round robin across organizations first, so it reorders only among equally loaded ones; 409 when the job already started (admin)",
		Auth: AuthWrite, Request: JobPriority{}, Response: jobs.Job{}},

	// Auth area (P2, handlers/auth.go, docs/auth.md). Local mode answers config and me only; the rest are hosted mode.
	{Method: "GET", Path: AuthPrefix + "/config", Tag: "auth", Summary: "Sign-in options: auth mode, signup policy, GitHub, bootstrap", Response: auth.AuthInfo{}},
	{Method: "GET", Path: AuthPrefix + "/me", Tag: "auth", Summary: "The caller: user, orgs with roles, current org, CSRF value; kind anonymous (200) with no credential, 401 for an ended session", Response: auth.Me{}},
	{Method: "POST", Path: AuthPrefix + "/signup", Tag: "auth", Summary: "Create an account under the signup policy and sign in", Request: auth.SignupRequest{}, Status: fasthttp.StatusCreated, Response: auth.Me{}},
	{Method: "POST", Path: AuthPrefix + "/login", Tag: "auth", Summary: "Sign in with email and password; sets the session cookie", Request: auth.LoginRequest{}, Response: auth.Me{}},
	{Method: "POST", Path: AuthPrefix + "/logout", Tag: "auth", Summary: "End the session and clear the cookie", Status: fasthttp.StatusNoContent},
	{Method: "POST", Path: AuthPrefix + "/switch-org", Tag: "auth", Summary: "Make one of the user's orgs the session's current org", Request: auth.SwitchOrgRequest{}, Response: auth.MeOrg{}},
	{Method: "POST", Path: AuthPrefix + "/password", Tag: "auth", Summary: "Change the password; ends the user's other sessions", Request: auth.PasswordChangeRequest{}, Status: fasthttp.StatusNoContent},
	{Method: "GET", Path: AuthPrefix + "/github/start", Tag: "auth", Summary: "Redirect to GitHub sign-in", Query: []QueryDoc{{Name: "return_to", Description: "Same-site path to land on after sign-in"}}, Status: fasthttp.StatusFound},
	{Method: "GET", Path: AuthPrefix + "/github/callback", Tag: "auth", Summary: "GitHub's redirect back; redirects to the return path or /login?error=", Status: fasthttp.StatusFound},
	{Method: "GET", Path: AuthPrefix + "/invitations/{token}", Tag: "auth", Summary: "Pending invitation: org, email, role, expiry", Response: auth.InvitationInfo{}},
	{Method: "POST", Path: AuthPrefix + "/invitations/{token}", Tag: "auth", Summary: "Accept an invitation as the signed-in user", Response: auth.MeOrg{}},
	{Method: "GET", Path: AuthPrefix + "/tokens", Tag: "auth", Summary: "API tokens of the current org (admin)", Response: TokenList{}},
	{Method: "POST", Path: AuthPrefix + "/tokens", Tag: "auth", Summary: "Create an API token; the secret is shown once (admin)", Request: auth.TokenCreateRequest{}, Status: fasthttp.StatusCreated, Response: auth.TokenCreated{}},
	{Method: "POST", Path: AuthPrefix + "/tokens/{id}/revoke", Tag: "auth", Summary: "Revoke an API token (admin)", Status: fasthttp.StatusNoContent},
	// W5 drift canary (canary.go, docs/drift-canary.md).
	{Method: "GET", Path: APIPrefix + "/canaries", Tag: "canaries", Summary: "Drift canaries with their state (steady, watching, drifted, invalid, baseline_stale), baseline, last look, schedule and spend today", Response: canary.List{}},
	{Method: "GET", Path: APIPrefix + "/canaries/{name}", Tag: "canaries", Summary: "One canary: definition, baselines, looks newest first with tests and what changed, provider equivalence, and history as trends lineages", Response: canary.Detail{}},
	{Method: "POST", Path: APIPrefix + "/canaries/{name}/run", Tag: "canaries", Summary: "Start one look (and its confirmation after a flag) in the background; 409 while the canary runs; refused under the daily cap",
		Auth: AuthWrite, Status: fasthttp.StatusAccepted, Response: canary.RunAccepted{}},
	// Improvement runs (improve.go, docs/improvement-runs.md).
	{Method: "GET", Path: APIPrefix + "/improvements", Tag: "improvements", Summary: "Improvement runs: the agent improving its own prompt, notes and skills over rounds, with status, commits and verdict", Response: improve.List{}},
	{Method: "POST", Path: APIPrefix + "/improvements", Tag: "improvements", Summary: "Create an improvement run from a spec (YAML or JSON) and start it in the background",
		Auth: AuthWrite, Request: improve.Spec{}, Status: fasthttp.StatusAccepted, Response: improve.Summary{}},
	{Method: "GET", Path: APIPrefix + "/improvements/{id}", Tag: "improvements", Summary: "The report: learning curves on dev, audit and held-out tasks against the control band, rounds, lineage, integrity and cost", Response: improve.Detail{}},
	{Method: "GET", Path: APIPrefix + "/improvements/{id}/rounds/{k}", Tag: "improvements", Summary: "One round: candidate, bundle diff, integrity checks, screen, commit test and held-out score", Response: improve.RoundView{}},
	{Method: "GET", Path: APIPrefix + "/improvements/{id}/lineage", Tag: "improvements", Summary: "The hash-chained version lineage, rejected candidates included, and whether the chain verifies", Response: improve.LineageView{}},
	{Method: "POST", Path: APIPrefix + "/improvements/{id}/rounds/{k}/approve", Tag: "improvements", Summary: "Override a paused round's tripwires with a reason and continue; the override is recorded",
		Auth: AuthWrite, Status: fasthttp.StatusAccepted, Request: ImproveApproveRequest{}, Response: improve.Summary{}},
	{Method: "POST", Path: APIPrefix + "/improvements/{id}/stop", Tag: "improvements", Summary: "Stop an improvement run", Auth: AuthWrite, Response: improve.Summary{}},
}

var (
	experimentMediaTypes = []string{"application/yaml"}
	gradeSourceQuery     = QueryDoc{Name: "grade_source", Description: "original (default) or a regrade id"}
	workerIDQuery        = QueryDoc{Name: "worker_id", Description: "Id the worker registered with"}
)

// OpenAPIHandler serves the OpenAPI description. It reads the routes the
// router holds when the first request arrives, after every handler has
// registered, so the document covers exactly what the server serves.
type OpenAPIHandler struct {
	mode    schemas.Mode
	version string
	routes  func() map[string][]string
	extra   map[string][]string // Tenancy area: the hosted platform routes served beside the org router
	hosted  bool                // Tenancy area: describe hosted session, API-token and runner auth
	once    sync.Once
	doc     []byte
	err     error
}

// NewOpenAPIHandler creates the description handler.
func NewOpenAPIHandler(mode schemas.Mode, version string) *OpenAPIHandler {
	return &OpenAPIHandler{mode: mode, version: version}
}

// ForHosted makes the document describe a hosted server (audit 8-C-05):
// platform lists the platform router's routes (accounts, orgs, jobs,
// runners), served beside the org router this handler is registered on, and
// the security schemes are the hosted session, API-token and runner ones
// instead of the local write and worker tokens.
func (h *OpenAPIHandler) ForHosted(platform map[string][]string) *OpenAPIHandler {
	h.hosted, h.extra = true, platform
	return h
}

// RegisterRoutes registers GET /openapi.json and keeps the router to list
// its routes later.
func (h *OpenAPIHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	h.routes = r.List
	r.GET(OpenAPIPath, lib.ChainMiddlewares(h.getOpenAPI, middlewares...))
}

// getOpenAPI handles GET /api/bench/openapi.json. The document is plain
// OpenAPI, so mode travels in X-Bench-Mode and x-bench-mode, not as a
// top-level "mode" key that would make the document invalid.
func (h *OpenAPIHandler) getOpenAPI(ctx *fasthttp.RequestCtx) {
	h.once.Do(func() {
		var doc map[string]any
		if h.hosted {
			doc, _ = BuildHostedOpenAPI(mergeRoutes(h.routes(), h.extra), h.version, h.mode)
		} else {
			doc, _ = BuildOpenAPI(h.routes(), h.version, h.mode)
		}
		h.doc, h.err = json.MarshalIndent(doc, "", "  ")
	})
	if h.err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "encode openapi: "+h.err.Error())
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json; charset=utf-8")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetBody(h.doc)
}

// BuildOpenAPI builds the OpenAPI 3.1 document for the registered routes
// (method -> router patterns, as router.List returns them). Only /api/
// routes are described. A registered route missing from APIRoutes still
// appears, as a bare operation, and is returned in undocumented as
// "METHOD /path"; the document lists them under x-bench-undocumented too.
// It is pure.
func BuildOpenAPI(registered map[string][]string, version string, mode schemas.Mode) (doc map[string]any, undocumented []string) {
	return buildOpenAPI(registered, version, mode, false)
}

// BuildHostedOpenAPI is BuildOpenAPI for a hosted server: registered is the
// org router's routes merged with the platform router's, OrgRouteDocs
// describe the org routes, and the security schemes are the hosted ones
// (audit 8-C-05).
func BuildHostedOpenAPI(registered map[string][]string, version string, mode schemas.Mode) (doc map[string]any, undocumented []string) {
	return buildOpenAPI(registered, version, mode, true)
}

// hostedRule is the tenancy rule the hosted dispatcher applies to a route:
// the platform router's entry, else the org router's (whose GETs without an
// entry read as viewer).
func hostedRule(method, pattern string) tenancy.Rule {
	pol := tenancy.DefaultPolicy()
	if r, ok := pol.PlatformRule(method, pattern); ok {
		return r
	}
	r, _ := pol.OrgRule(method, pattern)
	return r
}

// hostedSecurity sets an operation's security from its tenancy rule
// (QC2-20-03): public routes need nothing; account-only routes take the
// session cookie alone; org and signed-in routes take the session or an
// API token; runner routes take a runner token. The session's CSRF value
// is required only on unsafe methods, as auth middleware enforces it. An
// org read open to viewers is also open to anonymous callers on an org with
// public_read, shown as an empty requirement.
func hostedSecurity(op map[string]any, method string, rule tenancy.Rule) {
	session := map[string]any{"session": []string{}}
	if !tenancy.IsRead(method) {
		session["csrf"] = []string{}
	}
	token := map[string]any{"apiToken": []string{}}
	switch rule.Access {
	case tenancy.AccessPublic:
		op["security"] = []any{}
	case tenancy.AccessUser:
		op["security"] = []any{session}
	case tenancy.AccessSignedIn:
		op["security"] = []any{session, token}
	case tenancy.AccessRunner:
		op["security"] = []any{map[string]any{"runnerToken": []string{}}}
	case tenancy.AccessOrg:
		sec := []any{session, token}
		if tenancy.IsRead(method) && rule.Min == auth.RoleViewer {
			sec = append(sec, map[string]any{})
			op["x-heimdall-public-read"] = true
		}
		op["security"] = sec
		op["x-heimdall-min-role"] = string(rule.Min)
	}
}

// mergeRoutes unions two method -> patterns maps.
func mergeRoutes(a, b map[string][]string) map[string][]string {
	out := map[string][]string{}
	for _, m := range []map[string][]string{a, b} {
		for method, list := range m {
			for _, p := range list {
				if !slices.Contains(out[method], p) {
					out[method] = append(out[method], p)
				}
			}
		}
	}
	return out
}

func buildOpenAPI(registered map[string][]string, version string, mode schemas.Mode, hosted bool) (doc map[string]any, undocumented []string) {
	table := map[string]RouteDoc{}
	docs := APIRoutes
	if hosted {
		docs = append(append([]RouteDoc(nil), APIRoutes...), OrgRouteDocs...)
	}
	for _, rd := range docs {
		table[rd.Method+" "+rd.Path] = rd
	}
	g := newSchemaGen()
	g.hosted = hosted
	paths := map[string]map[string]any{}
	// Walk methods and paths in a fixed order: the first type to claim a
	// component name keeps it, so map order would make the document (and the
	// generated clients) differ from run to run.
	methods := make([]string, 0, len(registered))
	for method := range registered {
		methods = append(methods, method)
	}
	rank := map[string]int{"GET": 0, "POST": 1, "PUT": 2, "PATCH": 3, "DELETE": 4}
	slices.SortFunc(methods, func(a, b string) int {
		ra, oka := rank[a]
		rb, okb := rank[b]
		if !oka {
			ra = 9
		}
		if !okb {
			rb = 9
		}
		if ra != rb {
			return ra - rb
		}
		return strings.Compare(a, b)
	})
	for _, method := range methods {
		list := registered[method] // registration order, which is fixed
		for _, p := range list {
			if !strings.HasPrefix(p, "/api/") {
				continue
			}
			var rule *tenancy.Rule
			if hosted {
				r := hostedRule(method, p)
				if r.Access == tenancy.AccessDeny {
					continue // the hosted dispatcher refuses it for every caller (QC2-20-03)
				}
				rule = &r
			}
			rd, ok := table[method+" "+p]
			if !ok {
				undocumented = append(undocumented, method+" "+p)
				rd = RouteDoc{Method: method, Path: p, Tag: "undocumented", Summary: "Not yet described in handlers/openapi.go"}
			}
			op := g.operation(rd)
			if rule != nil {
				hostedSecurity(op, method, *rule)
			}
			key := openAPIPath(p)
			if paths[key] == nil {
				paths[key] = map[string]any{}
			}
			paths[key][strings.ToLower(method)] = op
		}
	}
	sort.Strings(undocumented)
	listed := []string{}
	listed = append(listed, undocumented...)
	g.components["ModeField"] = map[string]any{
		"type": "object", "required": []string{"mode"},
		"properties": map[string]any{"mode": map[string]any{
			"type": "string", "enum": []string{string(schemas.ModeLive), string(schemas.ModeRecorded), string(schemas.ModeSynthetic)},
			"description": "Where the data came from. Only live is a real measurement; recorded replays a stored bundle and synthetic is made up.",
		}},
	}
	doc = map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Heimdall bench API",
			"version":     cmp.Or(version, "dev"),
			"description": "Every JSON response is the payload object plus a top-level mode field. Raw responses carry mode in the X-Bench-Mode header.",
		},
		"paths": paths,
		"components": map[string]any{
			"schemas":         g.components,
			"securitySchemes": securitySchemes(hosted),
		},
		"x-bench-mode":         string(mode),
		"x-bench-undocumented": listed,
	}
	return doc, undocumented
}

// securitySchemes are the local tokens, or the hosted account, API-token
// and runner credentials (Tenancy area, tenancy/policy.go decides roles).
func securitySchemes(hosted bool) map[string]any {
	if hosted {
		return map[string]any{
			"session":     map[string]any{"type": "apiKey", "in": "cookie", "name": "heimdall_session", "description": "Signed-in account session; a write also sends the session's CSRF value in X-Heimdall-CSRF"},
			"csrf":        map[string]any{"type": "apiKey", "in": "header", "name": "X-Heimdall-CSRF", "description": "CSRF value of the session; required with the session cookie on writes"},
			"apiToken":    map[string]any{"type": "http", "scheme": "bearer", "description": "An organization API token; the org role decides what it may do"},
			"runnerToken": map[string]any{"type": "http", "scheme": "bearer", "description": "A runner token of the organization; required on the worker routes"},
		}
	}
	return map[string]any{
		"writeToken":  map[string]any{"type": "http", "scheme": "bearer", "description": "BENCH_WRITE_TOKEN; required on every write route"},
		"workerToken": map[string]any{"type": "http", "scheme": "bearer", "description": "BENCH_WORKER_TOKEN; required on the worker routes"},
		"inboxToken":  map[string]any{"type": "http", "scheme": "bearer", "description": "the inbox webhook token (token_env of the webhook source in inbox/sources.yaml); Failure inbox area"},
	}
}

// routeParam matches a router path parameter, with an optional regexp or
// catch-all suffix after a colon.
var routeParam = regexp.MustCompile(`\{([A-Za-z0-9_]+)(:[^}]*)?\??\}`)

// openAPIPath turns a router pattern into an OpenAPI path template.
func openAPIPath(p string) string {
	return routeParam.ReplaceAllString(p, "{$1}")
}

// schemaGen derives JSON Schemas from Go types into a shared components map.
type schemaGen struct {
	hosted     bool // Tenancy area: hosted security requirements
	components map[string]any
	names      map[reflect.Type]string
}

func newSchemaGen() *schemaGen {
	return &schemaGen{components: map[string]any{}, names: map[reflect.Type]string{}}
}

// operation builds one OpenAPI operation object.
func (g *schemaGen) operation(rd RouteDoc) map[string]any {
	op := map[string]any{
		"operationId": operationID(rd.Method, rd.Path),
		"summary":     rd.Summary,
		"tags":        []string{rd.Tag},
	}
	var params []any
	for _, m := range routeParam.FindAllStringSubmatch(rd.Path, -1) {
		params = append(params, map[string]any{"name": m[1], "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
	}
	for _, q := range rd.Query {
		params = append(params, map[string]any{"name": q.Name, "in": "query", "description": q.Description, "schema": map[string]any{"type": "string"}})
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	switch {
	case g.hosted:
		// Hosted security comes from the tenancy policy the dispatcher
		// enforces (hostedSecurity), not from the local token tags.
	case rd.Auth == AuthWrite:
		op["security"] = []any{map[string]any{"writeToken": []string{}}}
	case rd.Auth == AuthWorker:
		op["security"] = []any{map[string]any{"workerToken": []string{}}}
	case rd.Auth == AuthInbox: // Failure inbox area (inbox.go)
		op["security"] = []any{map[string]any{"inboxToken": []string{}}}
	}
	if body := g.requestBody(rd); body != nil {
		op["requestBody"] = body
	}
	op["responses"] = g.responses(rd)
	return op
}

// requestBody describes the request body, or returns nil.
func (g *schemaGen) requestBody(rd RouteDoc) map[string]any {
	content := map[string]any{}
	if rd.Request != nil {
		content["application/json"] = map[string]any{"schema": g.schema(reflect.TypeOf(rd.Request))}
	}
	for _, mt := range rd.RequestAlso {
		content[mt] = map[string]any{"schema": map[string]any{"type": "string"}}
	}
	if rd.RawRequest != "" {
		content[rd.RawRequest] = map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}
	}
	if len(content) == 0 {
		return nil
	}
	return map[string]any{"required": !rd.OptionalBody, "content": content}
}

// responses describes the success answer and the one error shape.
func (g *schemaGen) responses(rd RouteDoc) map[string]any {
	status := cmp.Or(rd.Status, fasthttp.StatusOK)
	ok := map[string]any{"description": fasthttp.StatusMessage(status)}
	content := map[string]any{}
	if rd.Response != nil {
		content["application/json"] = map[string]any{"schema": map[string]any{"allOf": []any{
			g.schema(reflect.TypeOf(rd.Response)), map[string]any{"$ref": "#/components/schemas/ModeField"},
		}}}
	}
	for _, mt := range []string{rd.Raw, rd.AlsoRaw} {
		if mt != "" {
			content[mt] = map[string]any{"schema": map[string]any{"type": "string"}}
		}
	}
	if len(content) > 0 {
		ok["content"] = content
	}
	out := map[string]any{fmt.Sprint(status): ok}
	if rd.NoContent && status != fasthttp.StatusNoContent {
		out["204"] = map[string]any{"description": fasthttp.StatusMessage(fasthttp.StatusNoContent)}
	}
	out["default"] = map[string]any{
		"description": "Error: {mode, error: {message, type}}",
		"content":     map[string]any{"application/json": map[string]any{"schema": g.schema(reflect.TypeOf(lib.ErrorBody{}))}},
	}
	return out
}

// operationID derives a stable id: method plus the path's static segments,
// with "By" before each parameter, e.g. getExperimentsByIdVerdict.
func operationID(method, route string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, seg := range strings.Split(strings.TrimPrefix(route, APIPrefix), "/") {
		if seg == "" {
			continue
		}
		if m := routeParam.FindStringSubmatch(seg); m != nil {
			b.WriteString("By")
			seg = m[1]
		}
		for _, part := range strings.FieldsFunc(seg, func(r rune) bool { return r == '_' || r == '-' || r == '.' }) {
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return b.String()
}

var (
	timeType      = reflect.TypeOf(time.Time{})
	rawJSONType   = reflect.TypeOf(json.RawMessage{})
	marshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
)

// customSchemas covers the types with their own JSON encoding, which
// reflection would describe wrongly.
var customSchemas = map[reflect.Type]map[string]any{
	reflect.TypeOf(schemas.TaskSelector{}): {
		"description": `"all", a list of task ids, or {"all": bool, "ids": [...]}`,
		"oneOf": []any{
			map[string]any{"type": "string", "enum": []string{"all"}},
			map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			map[string]any{"type": "object", "properties": map[string]any{
				"all": map[string]any{"type": "boolean"}, "ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}},
		},
	},
	reflect.TypeOf(schemas.MatrixModel{}): {
		"description": `a model name, or {"model", "provider"}`,
		"oneOf":       []any{map[string]any{"type": "string"}, map[string]any{"type": "object"}},
	},
}

// schema returns the JSON Schema of t, registering structs as components.
func (g *schemaGen) schema(t reflect.Type) map[string]any {
	if s, ok := customSchemas[t]; ok {
		return s
	}
	switch {
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case t == rawJSONType:
		return map[string]any{}
	case t.Kind() != reflect.Pointer && t.Kind() != reflect.Interface && (t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType)):
		return map[string]any{"description": "custom JSON encoding"}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return map[string]any{"anyOf": []any{g.schema(t.Elem()), map[string]any{"type": "null"}}}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}
		}
		return map[string]any{"type": "array", "items": g.schema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.schema(t.Elem())}
	case reflect.Struct:
		return map[string]any{"$ref": "#/components/schemas/" + g.component(t)}
	default:
		return map[string]any{}
	}
}

// component registers a struct type once and returns its component name.
// The name is reserved before the fields are walked, so recursive types end.
func (g *schemaGen) component(t reflect.Type) string {
	if name, ok := g.names[t]; ok {
		return name
	}
	name := componentName(t)
	if _, taken := g.components[name]; taken {
		// Same name in two packages: suffix the package name, keeping the
		// key a plain identifier that client generators can use as a type.
		name = componentName(t) + "_" + path.Base(t.PkgPath())
	}
	g.names[t] = name
	g.components[name] = map[string]any{}
	props, required := map[string]any{}, []string{}
	g.fields(t, props, &required)
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		slices.Sort(required)
		s["required"] = required
	}
	g.components[name] = s
	return name
}

// fields adds the JSON fields of struct t, flattening untagged embedded
// structs as encoding/json does.
func (g *schemaGen) fields(t reflect.Type, props map[string]any, required *[]string) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" || (!f.IsExported() && !f.Anonymous) {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		ft := f.Type
		if f.Anonymous && name == "" {
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				g.fields(ft, props, required)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		props[name] = g.schema(ft)
		if !strings.Contains(opts, "omitempty") && !strings.Contains(opts, "omitzero") {
			*required = append(*required, name)
		}
	}
}

// componentName is a type's name made safe for a component key.
func componentName(t reflect.Type) string {
	return strings.NewReplacer("[", "_", "]", "", "/", "_", ".", "_", "*", "", ",", "_", " ", "").Replace(t.Name())
}
