/**
 * Bifrost Bench wire types.
 *
 * MIRRORS THE GO TYPES IN bench/schemas. They change together: a field added, renamed or removed
 * there is added, renamed or removed here in the same change (bench/schemas/part.go says the same).
 * JSON keys are snake_case. Go `omitempty` fields are optional here; Go pointers are `| null`
 * only where the JSON can carry an explicit null.
 *
 * The "HTTP API payloads" section mirrors bench/schemas/api.go, the API contract. The Go test
 * bench/handlers/contract_test.go pins every route's keys to bench/handlers/testdata/contract/*.keys,
 * one file per type below; change a type here, its Go struct and its .keys file together.
 */

// ---------------------------------------------------------------------------------------------
// catalog.go: Mode
// ---------------------------------------------------------------------------------------------

/** Where the data on screen came from. Every API response carries it. */
export type Mode = "live" | "recorded" | "synthetic";

// ---------------------------------------------------------------------------------------------
// value.go
// ---------------------------------------------------------------------------------------------

export type ValueState = "observed" | "unknown" | "not_applicable";

/** Where a value was observed (DESIGN.md 9.5). */
export type EvidenceLevel = "outcome" | "gateway" | "native" | "boundary";

/** Every measured number. Unknown is never rendered or aggregated as zero. `v` is a number only when observed. */
export interface Value {
	state: ValueState;
	v: number | null;
	evidence?: EvidenceLevel;
	/** Smallest and largest value the unknown could take, in the metric's unit. */
	bounds?: [number, number];
	/** Machine reason code, for example "telemetry_incomplete". */
	reason?: string;
}

// ---------------------------------------------------------------------------------------------
// evidence.go: value kinds, metrics, gates, grade
// ---------------------------------------------------------------------------------------------

/** Selects the UI renderer and the aggregation for a metric. rate is 0..1, duration is seconds. */
export type ValueKind = "rate" | "count" | "tokens" | "usd" | "duration";

export interface MetricDescriptor {
	name: string;
	version: string;
	label: string;
	kind: ValueKind;
	higher_is_better: boolean;
	min_evidence: EvidenceLevel;
	description: string;
	/** One of the six metrics PS2 requires. */
	ps2: boolean;
}

/** What a fired gate does. */
export type GateEffect = "veto" | "invalidate" | "report" | "admission";

/** One gate's verdict on one run. */
export interface GateResult {
	gate: string;
	version: string;
	effect: GateEffect;
	fired: boolean;
	detail?: string;
	evidence?: string[];
}

/** The hidden verifier's result for one diff. */
export interface Grade {
	passed: boolean;
	invalid_evaluator: boolean;
	exit_code: number;
	tests_ran: number;
	failures: number;
	errors: number;
	skipped: number;
	missing_required?: string[];
	failed_tests?: string[];
	reason?: string;
	suite_version: string;
	grader_version: string;
	diff_sha256: string;
	/** Set on regrades. */
	parent_seal?: string;
}

// ---------------------------------------------------------------------------------------------
// run.go: slot and run (DESIGN.md 16.3)
// ---------------------------------------------------------------------------------------------

export interface Slot {
	slot_id: string;
	/** The slots of all arms for one (task, condition, trial). */
	pair_id: string;
	exp_id: string;
	suite: string;
	task: string;
	task_version: string;
	/** Arm id, or "compat" for the compatibility agent. */
	arm: string;
	/** "clean" or a probe id. */
	condition: string;
	/** 1-based. */
	trial: number;
	order_index: number;
	/** False for compatibility runs; they never enter the verdict. */
	counted: boolean;
}

/** Slot pipeline stages, in order (DESIGN.md 8.2). `run.stage` is the last completed one. */
export type Stage =
	| "planned"
	| "leased"
	| "prepare"
	| "arm"
	| "run"
	| "disarm"
	| "export"
	| "collect"
	| "grade"
	| "gate"
	| "measure"
	| "receipt"
	| "cleanup"
	| "seal";

/** pass = 1; fail, timeout, budget, crash, refusal, integrity_violation = 0; the rest are unknown or not counted. */
export type Outcome =
	| "pass"
	| "fail"
	| "timeout"
	| "budget"
	| "crash"
	| "refusal"
	| "integrity_violation"
	| "invalid_evaluator"
	| "infra_error"
	| "unknown"
	| "aborted_infra";

/** agent.go */
export interface Claim {
	/** A success claim was made. */
	made: boolean;
	/** Final message excerpt. Inert text. */
	text?: string;
	/** native, or outcome when read from stdout's tail. */
	source: EvidenceLevel;
	/** The phrase that counted as a claim. */
	matched?: string;
}

export interface RunTiming {
	started: string;
	agent_started?: string;
	agent_exited?: string;
	sealed?: string;
}

export interface RunAgentInfo {
	config_id: string;
	adapter: string;
	parser?: string;
	config_hash: string;
	exit_code?: number;
	timed_out: boolean;
}

export interface RunGatewayInfo {
	virtual_key_id?: string;
	key_slot?: string;
	session_id?: string;
}

export interface PartFailure {
	stage: Stage;
	part: string;
	error: string;
}

export interface Run {
	run_id: string;
	slot_id: string;
	pair_id: string;
	exp_id: string;
	/** 1-based; above 1 only after aborted_infra. */
	attempt: number;
	stage: Stage;
	manifest_hash: string;
	suite_lock_hash: string;
	outcome: Outcome;
	y: Value;
	claim?: Claim;
	timing: RunTiming;
	agent: RunAgentInfo;
	gateway: RunGatewayInfo;
	failures?: PartFailure[];
}

// ---------------------------------------------------------------------------------------------
// event.go: normalized event (DESIGN.md 16.4). Go: schemas.Event; named BenchEvent here so it
// does not shadow the DOM Event type.
// ---------------------------------------------------------------------------------------------

export type EventType =
	| "model_request"
	| "model_response"
	| "tool_call"
	| "tool_result"
	| "retry"
	| "fallback"
	| "fault"
	| "claim"
	| "grade"
	| "gate"
	| "compaction"
	| "error";

export type EventSource = "gateway" | "proxy_ledger" | "agent_stream" | "shim_log" | "grader" | "engine";

/** Four disjoint token buckets. */
export interface Tokens {
	fresh: number;
	cache_read: number;
	cache_write: number;
	out: number;
}

/** Event ids are "<run_id>:<source>:<seq>"; seq counts per source. */
export interface BenchEvent {
	event_id: string;
	run_id: string;
	slot_id: string;
	pair_id: string;
	seq: number;
	turn_index: number;
	ts: string;
	type: EventType;
	source: EventSource;
	evidence: EvidenceLevel;
	/** Bifrost row id. */
	request_id?: string;
	attempt_no?: number;
	tool_call_id?: string;
	parent_event_id?: string;
	tool_name?: string;
	/** File path, command or URL the action is about; used to align A and B. */
	target?: string;
	args_hash?: string;
	result_hash?: string;
	is_error?: boolean;
	provider?: string;
	model_requested?: string;
	model_served?: string;
	tokens?: Tokens;
	usd?: Value;
	/** Short inert text for display. Render as text only, never as HTML (strict CSP). */
	summary?: string;
}

// ---------------------------------------------------------------------------------------------
// probe.go: probes and receipts (DESIGN.md 10)
// ---------------------------------------------------------------------------------------------

export type ProbeLayerName = "fixture" | "gateway" | "tool_shim";

export type PS2Family = "unavailable_model" | "broken_tool" | "misleading_instruction";

export interface OverlaySpec {
	from: string;
	to: string;
}

export interface FaultPlan {
	/** none | persistent | once */
	mode: string;
	from_call?: number;
	call?: number;
	status?: number;
	retry_after_s?: number;
	nonce?: string;
	run_id?: string;
}

export interface ShimSpec {
	tool: string;
	/** crash | empty */
	mode: string;
	calls: number;
	exit_code: number;
	output?: string;
}

/** Only the block matching the probe's layer is set. */
export interface ProbeApply {
	overlay?: OverlaySpec;
	fault?: FaultPlan;
	shim?: ShimSpec;
}

export interface ProbeSpec {
	id: string;
	version: string;
	/** Diagnostics row label, for example memory, tools, resilience. */
	axis: string;
	ps2_family: PS2Family;
	layer: ProbeLayerName;
	goal_preserving: boolean;
	apply: ProbeApply;
	safe_behavior: string;
	metrics?: string[];
}

/** One rung of the receipt ladder, R0 to R7 in RECEIPT_STEPS order. */
export type ReceiptStep = "assigned" | "armed" | "fired" | "delivered" | "absorbed_by" | "visible" | "acknowledged" | "effect";

/** The ladder in order (Go: AllReceiptSteps). Index i is Ri. */
export const RECEIPT_STEPS: readonly ReceiptStep[] = [
	"assigned",
	"armed",
	"fired",
	"delivered",
	"absorbed_by",
	"visible",
	"acknowledged",
	"effect",
];

export type StepState = "yes" | "no" | "unknown" | "not_applicable";

export interface ReceiptEntry {
	state: StepState;
	/** For example the absorbed_by layer name, or a fired count. */
	detail?: string;
	/** Event ids, ledger hashes, Bifrost row ids. */
	evidence?: string[];
}

/** What a probe did in one run. A step the layer does not apply is not_applicable, never missing. */
export interface Receipt {
	probe_id: string;
	layer: ProbeLayerName;
	steps: Partial<Record<ReceiptStep, ReceiptEntry>>;
}

// ---------------------------------------------------------------------------------------------
// environment.go: diff
// ---------------------------------------------------------------------------------------------

export interface DiffFile {
	path: string;
	/** added | modified | deleted */
	status: string;
}

export interface DiffReject {
	path: string;
	/** symlink | device | submodule | outside_workspace | cache */
	reason: string;
}

export interface Diff {
	/** workspace.diff in the run folder. */
	path: string;
	sha256: string;
	files: DiffFile[];
	rejected?: DiffReject[];
}

// ---------------------------------------------------------------------------------------------
// experiment.go (DESIGN.md 16.6)
// ---------------------------------------------------------------------------------------------

/** Any JSON value; used where the Go side is `any` (arm overrides, config diff). */
export type JsonValue = string | number | boolean | null | JsonValue[] | { [key: string]: JsonValue };

export interface ArmSpec {
	label: string;
	/** Overrides the experiment's base config path. */
	agent?: string;
	/** Dotted-path overrides, for example {"notes.user_file": "fixtures/notes/codewins.md"}. */
	set?: Record<string, JsonValue>;
}

export interface Endpoint {
	condition: string;
	metric: string;
}

/** Rules: no_regression (on condition) and no_new_integrity_violation. */
export interface GuardSpec {
	condition?: string;
	rule: string;
	/** Defaults to the primary metric. */
	metric?: string;
}

/** All tasks of the suite, or an explicit list. */
export interface TaskSelector {
	all?: boolean;
	ids?: string[];
}

export interface ConditionSpec {
	/** "clean" or a probe id. */
	id: string;
	tasks: TaskSelector;
	trials: number;
}

export interface CompatibilitySpec {
	agent: string;
	tasks: TaskSelector;
	conditions: string[];
	trials: number;
}

export interface BudgetSpec {
	total_usd: number;
}

export interface VerdictSpec {
	/** paired_signflip */
	policy: string;
	alpha: number;
	assumed_sd: number;
}

export interface Experiment {
	/** e_<slug>; set by the planner when empty. */
	id: string;
	name: string;
	/** For example coding-core@1.0.0. */
	suite: string;
	base: string;
	arms: Record<string, ArmSpec>;
	primary: Endpoint;
	guards: GuardSpec[];
	conditions: ConditionSpec[];
	compatibility?: CompatibilitySpec;
	budget: BudgetSpec;
	verdict: VerdictSpec;
	metrics?: string[];
	extra_gates?: string[];
	seed: number;
	concurrency: number;
	/** agent_exposure | stack_resilience */
	manifest_mode: string;
}

// ---------------------------------------------------------------------------------------------
// verdict.go: verdict and analysis (DESIGN.md 9.3, 9.4, 9.6)
// ---------------------------------------------------------------------------------------------

export type Decision = "improved" | "regressed" | "inconclusive" | "invalid";

export type Action = "ship" | "hold" | "no_decision" | "fix_harness";

/** An undetermined guard blocks IMPROVED. */
export type GuardStatus = "pass" | "fail" | "undetermined";

/** Sits beside the verdict. A regression never shows green. unknown: no gate data (an audit of published counts). */
export type IntegrityStatus = "clean" | "alert" | "regressed" | "unknown";

/** One task's paired difference. */
export interface TaskDiff {
	task: string;
	mean_a: number;
	mean_b: number;
	d: number;
	n_a: number;
	n_b: number;
	unknown_a: number;
	unknown_b: number;
}

/** Paired per-task differences for one condition and metric. d_bar is a fraction (0.33 = +33 pp). */
export interface Effect {
	condition: string;
	metric: string;
	arm_a: string;
	arm_b: string;
	n_tasks: number;
	n_nonzero: number;
	n_positive: number;
	n_negative: number;
	d_bar: number;
	/** Exact sign-flip, two-sided; absent when descriptive. */
	p_value?: number;
	/** Paired t, descriptive only, always labelled approximate. */
	interval?: [number, number];
	per_task: TaskDiff[];
	/** Unknowns set against B. */
	lower_bound?: Effect;
	/** Unknowns set in favour of B. */
	upper_bound?: Effect;
	descriptive: boolean;
}

export interface GuardResult {
	spec: GuardSpec;
	status: GuardStatus;
	effect?: Effect;
	detail?: string;
}

/** The release decision on the primary endpoint. */
export interface Verdict {
	primary: Endpoint;
	decision: Decision;
	action: Action;
	effect?: Effect;
	alpha: number;
	/** Minimal detectable effect in percentage points. */
	mde: number;
	mde_assumed_sd: boolean;
	sd: number;
	/** Tasks a new experiment needs to detect |d_bar|. */
	tasks_needed?: number;
	/** Set when even this many tasks would not be enough. */
	tasks_needed_over?: number;
	/** "task" */
	randomization_unit: string;
	tie_rule: string;
	guards: GuardResult[];
	invalid_reasons?: string[];
	/** "unknown" or "" on an audit verdict: a published table says nothing about veto gates. */
	integrity: IntegrityStatus | "";
	manifest_hash: string;
	frozen_at: string;
}

/** A count over a denominator with the unknowns kept apart. */
export interface Ratio {
	num: number;
	den: number;
	unknown: number;
}

/** One condition's behaviour per arm. Rows come from the catalog. */
export interface DiagnosticsRow {
	condition: string;
	axis: string;
	n_tasks: number;
	/** arm -> successes over runs */
	per_arm: Record<string, Ratio>;
	effect?: Effect;
	descriptive: boolean;
	/** arm -> receipt step -> yes over applicable */
	receipts?: Record<string, Partial<Record<ReceiptStep, Ratio>>>;
}

/** An always-on aggregate (honesty, safety, efficiency). */
export interface Meter {
	name: string;
	label: string;
	kind: ValueKind;
	/** Definition, for example "claims among failures". */
	numerator: string;
	denominator: string;
	per_arm: Record<string, Value>;
}

/** A derived, recomputable result over sealed runs (analysis/<analysis_id>/). */
export interface Analysis {
	analysis_id: string;
	exp_id: string;
	created_at: string;
	/** "original" or a regrade id. */
	grade_source: string;
	mode: Mode;
	verdict: Verdict;
	/** arm -> raw passes over runs: the naive scoreboard. */
	naive: Record<string, Ratio>;
	diagnostics: DiagnosticsRow[];
	meters: Meter[];
	/** arm -> metric -> median per run */
	arm_metrics: Record<string, Record<string, Value>>;
}

/** A sealed run as analysis reads it, with the grade source chosen. */
export interface SealedRun {
	slot: Slot;
	run: Run;
	dir: string;
	seal_hash: string;
	grade?: Grade;
	grade_source: string;
	metrics: Record<string, Value>;
	gates: GateResult[];
	receipts?: Receipt[];
}

// ---------------------------------------------------------------------------------------------
// catalog.go (DESIGN.md 8.1 rule 6). Drives the UI.
// ---------------------------------------------------------------------------------------------

export interface CatalogSuite {
	id: string;
	version: string;
	tasks: string[];
	probes: string[];
	lock_hash?: string;
}

export interface CatalogAgent {
	id: string;
	path: string;
	adapter: string;
	model: string;
	parser?: string;
}

export interface CatalogGate {
	name: string;
	version: string;
	effect: GateEffect;
	/** Baseline gates cannot be switched off. */
	baseline: boolean;
}

export interface Catalog {
	mode: Mode;
	suites: CatalogSuite[];
	probes: ProbeSpec[];
	agents: CatalogAgent[];
	metrics: MetricDescriptor[];
	gates: CatalogGate[];
	/** "<kind>/<name>" -> version */
	parts: Record<string, string>;
}

// =============================================================================================
// api.go: HTTP API payloads (DESIGN.md 16.7)
// =============================================================================================

/** `mode` sits at the top level of every response object, beside the payload. */
export type WithMode<T> = T & { mode: Mode };

/** One typed config change between arm A and arm B, from the arms' `set:` overrides (DESIGN.md 8.6). */
export interface ConfigChange {
	/** Dotted agent-config path, for example "notes.user_file". */
	path: string;
	a: JsonValue;
	b: JsonValue;
}

/** One gate over all runs, for the verdict screen's gate checklist. */
export interface GateSummary {
	gate: string;
	version: string;
	effect: GateEffect;
	baseline: boolean;
	/** Runs the gate fired on over runs checked. */
	fired: Ratio;
	detail?: string;
}

/** GET /experiments/{id}/verdict */
export interface VerdictPayload {
	exp_id: string;
	analysis_id: string;
	grade_source: string;
	suite: string;
	verdict: Verdict;
	naive: Record<string, Ratio>;
	gates: GateSummary[];
	config_diff: ConfigChange[];
}

/** GET /experiments/{id}/diagnostics */
export type DiagnosticsPayload = Pick<Analysis, "analysis_id" | "exp_id" | "diagnostics" | "meters" | "arm_metrics">;

export interface GridCell {
	slot_id: string;
	pair_id: string;
	/** Null until the slot has a counted run. */
	run_id: string | null;
	arm: string;
	trial: number;
	outcome: Outcome | null;
	y: Value;
}

export interface GridRow {
	task: string;
	condition: string;
	diff: TaskDiff;
	discordant: boolean;
	cells: GridCell[];
}

/** GET /experiments/{id}/grid: task x trial outcomes, discordant rows first. */
export interface Grid {
	exp_id: string;
	arms: string[];
	conditions: string[];
	trials: number;
	rows: GridRow[];
}

export interface PairRun {
	arm: string;
	slot: Slot;
	run: Run;
	metrics: Record<string, Value>;
}

/** Events of each arm matched on (event type, target). Null where a lane has no event. */
export interface AlignedStep {
	index: number;
	type: EventType;
	target: string;
	events: Record<string, string | null>;
	diverged: boolean;
}

export interface FirstDivergence {
	index: number;
	/** An observation, never a root cause (DESIGN.md 11). */
	note: string;
}

/** GET /pairs/{pair_id} */
export interface Pair {
	pair_id: string;
	exp_id: string;
	task: string;
	condition: string;
	trial: number;
	runs: PairRun[];
	alignment: AlignedStep[];
	first_divergence: FirstDivergence | null;
}

/** GET /runs/{id}: one run with its seal, grade, metrics, gates and receipts. Unwritten files are null or empty. */
export interface RunDetail {
	slot: Slot;
	run: Run;
	sealed: boolean;
	/** Empty until the run is sealed. */
	seal_hash: string;
	grade: Grade | null;
	/** "original": the grade the run was sealed with. */
	grade_source: string;
	metrics: Record<string, Value>;
	gates: GateResult[];
	receipts: Receipt[];
}

/** GET /runs/{id}/events */
export interface RunEvents {
	run_id: string;
	/** False when the gateway could not return every request row. Shown, never hidden. */
	complete: boolean;
	events: BenchEvent[];
}

/** GET /runs/{id}/diff */
export interface RunDiff {
	run_id: string;
	diff: Diff;
	/** Unified diff text. Inert: render as text only. */
	patch: string;
}

/** GET /runs/{id}/receipts */
export interface RunReceipts {
	run_id: string;
	receipts: Receipt[];
}

export interface PlanCondition {
	condition: string;
	tasks: number;
	trials: number;
	slots: number;
}

/** POST /experiments/plan. Nothing runs. */
export interface ExperimentPlan {
	slots: number;
	pairs: number;
	conditions: PlanCondition[];
	/** Minimal detectable effect on the primary endpoint, percentage points. */
	mde: number;
	mde_assumed_sd: boolean;
	sd: number;
	est_wall_s: Value;
	est_usd: Value;
	/** Nothing can start while non-empty (identical arms, overlapping arm and probe files). */
	errors: string[];
	warnings: string[];
}

/** POST /experiments: the manifest is frozen and hashed. */
export interface ExperimentCreated {
	exp_id: string;
	manifest_hash: string;
	created_at: string;
}

export type ExperimentState = "created" | "running" | "analyzing" | "done" | "failed" | "cancelled";

export interface SlotProgress {
	total: number;
	done: number;
	running: number;
	failed: number;
}

/** GET /experiments/{id}, POST /experiments/{id}/start */
export interface ExperimentStatus {
	exp_id: string;
	name: string;
	state: ExperimentState;
	manifest_hash: string;
	frozen_at: string;
	started_at?: string;
	finished_at?: string;
	slots: SlotProgress;
	spent_usd: Value;
	analysis_id?: string;
}

/** GET /experiments */
export interface ExperimentList {
	experiments: ExperimentStatus[];
}

/** One server-sent event on GET /experiments/{id}/events. */
export interface LiveEvent {
	seq: number;
	/** slot_started | stage | slot_sealed | budget_stop | experiment_done | error | end */
	kind: string;
	slot_id?: string;
	run_id?: string;
	stage?: Stage;
	outcome?: Outcome;
	message?: string;
	at: string;
}

/** GET /experiments/{id}/events without text/event-stream: the events after ?since=<seq>. */
export interface EventsPoll {
	exp_id: string;
	events: LiveEvent[];
	/** The ?since= of the next poll. */
	next: number;
	/** Older events fell out of the server's window. */
	truncated: boolean;
	status: ExperimentStatus;
}

/** GET /health */
export interface Health {
	status: "ok";
	version: string;
}

export type DoctorStatus = "pass" | "fail" | "warn" | "skip";

export interface DoctorCheck {
	id: string;
	status: DoctorStatus;
	detail: string;
}

/** GET /doctor */
export interface Doctor {
	ok: boolean;
	checks: DoctorCheck[];
}

/** One task of a published two-arm comparison; the columns of verdict.ParseAuditCSV. */
export interface AuditRow {
	task: string;
	a_pass: number;
	a_n: number;
	b_pass: number;
	b_n: number;
}

/** POST /audit. The response is a Verdict (verdict.Audit) on the endpoint published/pass. */
export interface AuditRequest {
	rows: AuditRow[];
	alpha?: number;
}

export interface RegradeRequest {
	exp_id: string;
	suite_version: string;
}

/** POST /regrade (stretch) */
export interface RegradeResult {
	regrade_id: string;
	exp_id: string;
	runs: number;
}

// ---------------------------------------------------------------------------------------------
// HTTP responses (DESIGN.md 16.7). Every one carries `mode`.
// ---------------------------------------------------------------------------------------------

export type HealthResponse = WithMode<Health>;
export type DoctorResponse = WithMode<Doctor>;
export type CatalogResponse = Catalog;
export type PlanResponse = WithMode<ExperimentPlan>;
export type ExperimentCreatedResponse = WithMode<ExperimentCreated>;
export type ExperimentResponse = WithMode<ExperimentStatus>;
export type ExperimentListResponse = WithMode<ExperimentList>;
export type EventsPollResponse = WithMode<EventsPoll>;
export type VerdictResponse = WithMode<VerdictPayload>;
export type DiagnosticsResponse = WithMode<DiagnosticsPayload>;
export type GridResponse = WithMode<Grid>;
export type RunResponse = WithMode<RunDetail>;
export type RunEventsResponse = WithMode<RunEvents>;
export type RunDiffResponse = WithMode<RunDiff>;
export type RunReceiptsResponse = WithMode<RunReceipts>;
export type PairResponse = WithMode<Pair>;
export type AuditResponse = WithMode<Verdict>;
export type RegradeResponse = WithMode<RegradeResult>;