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

export type EvidenceSource =
	| "hidden_grader"
	| "bifrost_log"
	| "fault_proxy"
	| "tool_runner"
	| "agent_stream"
	| "workspace_diff"
	| "process_result"
	| "inferred"
	| "unknown";

export type EvidenceStrength = "verified" | "strong" | "partial" | "inferred" | "unknown";

export interface Budgets {
	wall_s: number;
	turns?: number;
	cpus: number;
	mem_gb: number;
	pids_limit?: number;
	usd?: number;
	max_retries?: number;
	max_tool_calls?: number;
}

export interface GatewayPolicy {
	provider: string;
	max_retries: number;
	fallbacks?: string[];
}

export interface AdapterCapabilities {
	supports_streaming?: boolean;
	supports_structured_events?: boolean;
	supports_tool_events?: boolean;
	supports_notes_file?: boolean;
	supports_model_override?: boolean;
	supports_cancellation?: boolean;
	supports_resume?: boolean;
	supports_gateway_headers?: boolean;
	task_types?: string[];
	evidence_levels?: EvidenceLevel[];
}

export type EvidenceQuality = "unknown" | "complete" | "partial";

export interface RunEvidenceContract {
	required?: string[];
	available?: string[];
	missing?: string[];
	quality: EvidenceQuality;
	sources?: EvidenceSource[];
	optional_missing?: string[];
}

/** Every measured number. Unknown is never rendered or aggregated as zero. `v` is a number only when observed. */
export interface Value {
	state: ValueState;
	v: number | null;
	evidence?: EvidenceLevel;
	source?: EvidenceSource;
	evidence_level?: EvidenceStrength;
	/** Smallest and largest value the unknown could take, in the metric's unit. */
	bounds?: [number, number];
	/** Machine reason code, for example "telemetry_incomplete". */
	reason?: string;
}

export interface CanonicalTaskIdentity {
	suite_id: string;
	suite_version: string;
	task_id: string;
	task_version: string;
	task_hash: string;
	condition: string;
	trial: number;
}

export interface RunInputIdentity {
	task_hash: string;
	run_input_hash: string;
	condition: string;
	probe_hash?: string;
	arm_config_hash: string;
	model_requested?: string;
	gateway_provider?: string;
	gateway_policy_hash?: string;
	environment_policy_hash?: string;
}

export interface CanonicalArmIdentity {
	agent_config_id: string;
	agent_config_hash: string;
	adapter_version?: string;
	agent_version?: string;
	image_digest?: string;
	parser_version?: string;
	model_requested?: string;
	gateway_provider?: string;
	gateway_policy: GatewayPolicy;
	budgets: Budgets;
	budget_snapshot?: BudgetSnapshot;
	capabilities?: AdapterCapabilities;
}

export interface BudgetSnapshot {
	configured: Budgets;
	effective: Budgets;
	source?: Record<string, string>;
}

export type BudgetParity = "equal" | "declared_difference" | "unexpected_difference";

export interface BudgetParityResult {
	status: BudgetParity;
	diffs?: Array<{ field: string; per_arm: Record<string, string> }>;
	configured_diffs?: Array<{ field: string; per_arm: Record<string, string> }>;
	effective_diffs?: Array<{ field: string; per_arm: Record<string, string> }>;
}

export interface MetricEvidence {
	name: string;
	value: Value;
	source: EvidenceSource;
	evidence_level: EvidenceStrength;
	unknown_reason?: string;
}

export interface LogicalRequest {
	logical_request_id: string;
	logical_step: number;
	request_id?: string;
	status?: string;
}

export interface AttemptRecord {
	attempt_id: string;
	logical_request_id: string;
	attempt_number: number;
	reason?: string;
	provider?: string;
	model?: string;
	status?: string;
	retry_type?: string;
}

export interface RetryRecord extends AttemptRecord {
	retry_type: string;
}

export interface FallbackRecord {
	fallback_index: number;
	logical_request_id: string;
	requested_provider?: string;
	served_provider?: string;
	requested_model?: string;
	served_model?: string;
	reason?: string;
}

export interface ToolCallRecord {
	tool_call_id: string;
	logical_step: number;
	tool_name?: string;
	requested: boolean;
	executed: boolean;
	request_source: EvidenceSource;
	execution_source: EvidenceSource;
	evidence_level: EvidenceStrength;
	execution_id?: string;
	unknown_reason?: string;
}

export interface ConflictRecord {
	conflict_type: string;
	sources: EvidenceSource[];
	values: string[];
	resolution: string;
	confidence: EvidenceStrength;
}

export interface EvidenceStatus {
	level: EvidenceStrength;
	sources?: EvidenceSource[];
	unknown_reasons?: string[];
	conflicts?: ConflictRecord[];
}

export interface CanonicalRun {
	canonical_version: string;
	schema_version: string;
	generated_at: string;
	run_id: string;
	experiment: string;
	task: CanonicalTaskIdentity;
	run_input: RunInputIdentity;
	arm: string;
	agent: CanonicalArmIdentity;
	budgets: BudgetSnapshot;
	model_requested?: string;
	model_served?: string;
	events?: BenchEvent[];
	grade?: Grade;
	gates?: GateResult[];
	metrics?: Record<string, Value>;
	evidence_status: EvidenceStatus;
	logical_steps?: LogicalRequest[];
	attempts?: AttemptRecord[];
	retries?: RetryRecord[];
	fallbacks?: FallbackRecord[];
	tool_calls?: ToolCallRecord[];
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
	/** How the grade was reached when it is not a clean-room diff grade: snapshot, answer or state. */
	mode?: string;
	/** A judge model decided `passed` (a judged suite). */
	judged?: boolean;
	judge?: JudgeRecord;
	/** The task's answer category, for breakdowns. */
	category?: string;
}

/** What one judged grade rested on (schemas.JudgeRecord). The judge's cost is never the agent's. */
export interface JudgeRecord {
	model: string;
	prompt_version: string;
	prompt_sha256: string;
	/** Each call's verdict: correct, incorrect or unparsable. */
	votes: string[];
	usd: Value;
	/** Price snapshot the judge cost was priced from. */
	price_ref?: string;
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
	task_hash?: string;
	run_input_hash?: string;
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
	capability_gaps?: string[];
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
	task_hash?: string;
	run_input_hash?: string;
	outcome: Outcome;
	y: Value;
	claim?: Claim;
	timing: RunTiming;
	agent: RunAgentInfo;
	arm_identity?: CanonicalArmIdentity;
	budget_snapshot?: BudgetSnapshot;
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

/** Rules: no_regression (on condition), no_new_integrity_violation and timeout_parity (holds, never regresses). */
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

export interface CapabilityRequirements {
	required?: string[];
	optional?: string[];
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
	requirements?: CapabilityRequirements;
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
	/** Measured instrument checks; a failed one is also an invalid reason. Absent on an audit verdict. */
	instruments?: InstrumentCheck[];
	/** "unknown" or "" on an audit verdict: a published table says nothing about veto gates. */
	integrity: IntegrityStatus | "";
	manifest_hash: string;
	frozen_at: string;
}

/** fail makes the experiment INVALID; unknown is never a pass and holds an IMPROVED verdict; warn changes nothing unless declared as a guard. */
export type InstrumentStatus = "pass" | "fail" | "unknown" | "warn" | "not_applicable";

/** One check that the measurement itself worked (usage_completeness, dropped_logs, timeout_parity, budget_parity, upstream_errors). */
export interface InstrumentCheck {
	name: string;
	status: InstrumentStatus;
	threshold: string;
	/** arm -> count over denominator, when the check is per arm. */
	per_arm?: Record<string, Ratio>;
	detail: string;
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
	/** The release policy result; absent when the experiment has no policy. */
	policy?: PolicyResult;
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
	/** From evidence.json; absent when the run has no gateway log. */
	gateway?: GatewayTally;
}

/** What analysis reads from a run's gateway log: usage completeness and the global drop counter. */
export interface GatewayTally {
	/** Message rows that should carry usage: every one except error rows. */
	requests: number;
	with_usage: number;
	complete: boolean;
	/** Bifrost's global drop counter at collect time; absent when not read. */
	dropped_global?: number;
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
	/** The release policy result; absent when the experiment has no policy. */
	policy?: PolicyResult;
}

// ---------------------------------------------------------------------------------------------
// policy.go: release policy as code. A policy only adds requirements; the baseline gates always run.
// ---------------------------------------------------------------------------------------------

/** One rule of a policies/<name>.yaml file. Exactly one of rule, max, min or equals. */
export interface PolicyCheck {
	id: string;
	description?: string;
	/** A per-run metric name, or an aggregate: integrity_violations, cost_per_verified_success. */
	metric: string;
	/** Default: the primary condition; "all" for every condition. */
	condition?: string;
	/** Default B, the candidate. */
	arm?: string;
	rule?: "no_regression" | "improved";
	max?: number;
	min?: number;
	equals?: number;
	min_n?: number;
	severity?: "block" | "warn";
}

/** A release policy file (schemas.Policy). */
export interface Policy {
	policy_version: number;
	name: string;
	description?: string;
	on_unknown: "block" | "warn";
	checks: PolicyCheck[];
}

/** One check's result. Unknown is never a pass. */
export type PolicyStatus = "pass" | "fail" | "unknown";

/** The policy's overall result. block means the release must not ship. */
export type PolicyDecision = "allow" | "warn" | "block";

/** Threshold texts of the two rules (policy.ThresholdNoRegression, policy.ThresholdImproved); their observed value is d_bar. */
export const POLICY_RULE_THRESHOLDS: readonly string[] = ["no regression", "improved"];

/** One check's result with its evidence (schemas.PolicyCheckResult). */
export interface PolicyCheckResult {
	id: string;
	metric: string;
	condition: string;
	arm: string;
	status: PolicyStatus;
	/** This result alone blocks the release. */
	blocking: boolean;
	/** The arm's mean, the aggregate, or for a rule the release effect d_bar (a fraction). */
	observed: Value;
	/** For example "<= 0.05" or "no regression". */
	threshold: string;
	/** Observed runs behind `observed`. */
	n: number;
	reason?: string;
	/** Run ids that break the rule, or whose value is unknown; at most 20. */
	evidence?: string[];
}

/** The evaluation of a policy over one analysis (schemas.PolicyResult). */
export interface PolicyResult {
	policy_name: string;
	/** sha256 of the policy file as evaluated. */
	policy_hash: string;
	decision: PolicyDecision;
	checks: PolicyCheckResult[];
	evaluated_at: string;
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
	/** Why a sealed run's files no longer match its seal.json or seals.log; absent when they match or the run is not sealed. */
	seal_error?: string;
	// Scenario evidence (scenario.go). Each is absent for a plain diff run.
	/** The tool proxy ledger, in call order. */
	tool_calls?: ToolCall[];
	/** One per session of a multi-session run, in order. */
	sessions?: SessionResult[];
	answer?: Answer;
	/** Service state exported after the agent exited. */
	state?: StateFile[];
	/** Services that ran beside the agent. */
	services?: ServiceResult[];
	evidence_contract?: RunEvidenceContract;
	canonical?: CanonicalRun;
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

/** One observed failure pattern across an experiment (schemas.TriageGroup). An observed pattern, never a root cause. */
export interface TriageGroup {
	/** Metric name, for example "pattern_loop". */
	pattern: string;
	label: string;
	description: string;
	/** arm -> runs showing the pattern over runs with an observed value; unknowns kept apart. */
	per_arm: Record<string, Ratio>;
	/** arm -> run ids showing the pattern. */
	runs: Record<string, string[]>;
}

/** GET /experiments/{id}/triage */
export interface Triage {
	exp_id: string;
	groups: TriageGroup[];
}

// ---------------------------------------------------------------------------------------------
// policy.go: release log. A hash-chained record of a decision, not a signature or a certification.
// ---------------------------------------------------------------------------------------------

/** schemas.PolicyDecision as a release records it. block means the release must not ship without an override. */
export type ReleasePolicyDecision = "allow" | "warn" | "block";

/** One entry of the hash-chained release log (schemas.ReleaseRecord). */
export interface ReleaseRecord {
	release_id: string;
	exp_id: string;
	analysis_id: string;
	/** The arm released, normally B. */
	arm: string;
	agent_config_id: string;
	config_hash: string;
	decision: Decision;
	action: Action;
	policy_name?: string;
	policy_hash?: string;
	/** Absent when no policy was evaluated. */
	policy_decision?: ReleasePolicyDecision;
	manifest_hash: string;
	/** sha256 over every counted run's seal hash, sorted. */
	seals_digest: string;
	approver: string;
	note?: string;
	/** Set only when released against a non-ship action or a policy block, with the reason. */
	override?: string;
	/** Relative to the releases root. */
	evidence_path?: string;
	evidence_sha256?: string;
	created_at: string;
	prev_hash: string;
	hash: string;
}

/** POST /releases */
export interface ReleaseRequest {
	exp_id: string;
	/** Default: the latest analysis. */
	analysis_id?: string;
	approver: string;
	note?: string;
	/** Required to release against a non-ship action or a policy block. */
	override?: string;
}

/** GET /releases, in log order (oldest first). */
export interface ReleaseList {
	releases: ReleaseRecord[];
	chain_ok: boolean;
}

// ---------------------------------------------------------------------------------------------
// leaderboard.go: agent configurations ranked by verified success. No blended score.
// ---------------------------------------------------------------------------------------------

/** all: each entry over its own tasks. common: every entry over common_tasks only. */
export type LeaderboardScope = "all" | "common";

/** exclude: live runs only. only: scripted agent or scripted model runs only. */
export type LeaderboardSynthetic = "exclude" | "only";

/** original: every run as sealed. latest: each experiment's newest regrade, where it has a result for the run. */
export type LeaderboardGradeSourceMode = "original" | "latest";

/**
 * One agent configuration on the board (schemas.LeaderboardEntry), one per resolved config hash.
 * Rank alone is never a claim that one agent beats another: the tier says whether the entry can
 * be told apart from the leader.
 */
export interface LeaderboardEntry {
	/** 1-based position by success, ties broken by lower cost per success, then more runs. */
	rank: number;
	/** 1: cannot be told apart from the leader. 2: significantly behind the leader. */
	tier: number;
	/** Holm-adjusted two-sided exact sign-flip p against the leader on common tasks; null for the leader. */
	p_vs_leader: number | null;
	/** Plain words, for example "leader", "p 0.62 on 8 common tasks". */
	tier_reason: string;
	/** sha256 of the resolved agent config. */
	config_hash: string;
	/** Agent config id, for example "codex". */
	agent: string;
	model: string;
	/** Arm label from the newest contributing experiment. */
	label: string;
	/** Scripted agent or scripted model: the numbers are not findings. */
	synthetic: boolean;
	/** Experiment ids that contributed runs, newest first. */
	experiments: string[];
	/** Distinct tasks with at least one graded run. */
	tasks: number;
	/** Graded runs counted. */
	runs: number;
	/** The k of reliability: min(3, the fewest trials per task among its experiments that repeat tasks), unless the query sets k. */
	trials: number;
	/** Mean over tasks of the per-task verified success rate. Bounds: descriptive 95% t interval, absent with fewer than 2 tasks. */
	success: Value;
	/** Verified successes over graded runs, pooled. */
	passes: Ratio;
	/** Mean over tasks of pass^k with k = trials. Unknown when trials < 2 or a task has fewer than trials graded runs. */
	reliability: Value;
	/**
	 * Per-run aggregates by metric id: medians for steps, tool_calls, tokens, cost_usd, wall_time_s;
	 * shares of runs for false_completion (over runs whose outcome and claim are known, a passing run counting as 0)
	 * and safe_success; cost_per_success (not_applicable with no success).
	 */
	metrics: Record<string, Value>;
	/** Fault condition -> mean over shared tasks of (success under it minus success clean), a fraction. Only on a clean board. */
	resilience: Record<string, Value>;
}

/** Runs the board left out for one reason, for example "infra_error" or "not sealed". */
export interface LeaderboardExclusion {
	reason: string;
	runs: number;
	experiments: string[];
}

/** The grade source one experiment's runs on the board were read from (schemas.LeaderboardGradeSource). */
export interface LeaderboardGradeSource {
	experiment: string;
	/** "original", or the regrade id read: the experiment's newest regrade by created_at, ties by the larger id. */
	source: string;
	/** The experiment's runs on the board, graded or excluded. */
	runs: number;
	/** Of runs, those read from the regrade; the rest keep their original grade. */
	regraded_runs: number;
}

/** GET /leaderboard: every sealed run of one suite version under one condition, across experiments. */
export interface Leaderboard {
	/** id@version this board covers. */
	suite: string;
	/** Every id@version with sealed runs, newest first. */
	suites: string[];
	condition: string;
	/** Conditions with sealed runs in this suite. */
	conditions: string[];
	scope: LeaderboardScope;
	synthetic: LeaderboardSynthetic;
	/** Tasks every listed entry ran under the condition. */
	common_tasks: string[];
	/** Significance level for tiers (0.05). */
	alpha: number;
	/** Always verified_success. */
	sort_metric: string;
	/** Ordered by rank. */
	entries: LeaderboardEntry[];
	/** Runs left out, grouped by reason. */
	excluded: LeaderboardExclusion[];
	generated_at: string;
	/** What each run's outcome and metrics were read from. */
	grade_source: LeaderboardGradeSourceMode;
	/** Per experiment with runs on the board, the source its runs were read from, newest experiment first. */
	grade_sources: LeaderboardGradeSource[];
}

/** Query of GET /leaderboard. An absent field takes the server default: newest suite, clean, all, exclude, original, each entry's k. */
export interface LeaderboardParams {
	suite?: string;
	condition?: string;
	scope?: LeaderboardScope;
	synthetic?: LeaderboardSynthetic;
	grade_source?: LeaderboardGradeSourceMode;
	/** k of the pass^k reliability for every entry, 2 or more. */
	k?: number;
}

// ---------------------------------------------------------------------------------------------
// suitesapi.go: the benchmark packs and agent configs on disk, read-only. These payloads never
// carry a hidden test, a reference solution, an admission control, a hidden test name, an
// admission check's raw detail, a probe's markers or an environment value.
// ---------------------------------------------------------------------------------------------

/** locked: the lock matches the files. stale: files changed since. unknown: the lock could not be checked. */
export type SuiteLockState = "locked" | "unlocked" | "stale" | "unknown";

/** How a suite's verifier reports (SuiteVerifier.Result). */
export type VerifierResultFormat = "junit" | "reward" | "exit_code";

/** The family an admission check belongs to. */
export type AdmissionControl =
	| "reference"
	| "empty"
	| "mutant"
	| "exploit"
	| "null_agent"
	| "stale_notes"
	| "secrets"
	| "network"
	| "other";

/** A task's lock status, or not_locked when the task is not in suite.lock. */
export type SuiteTaskStatus = "admitted" | "draft" | "quarantined" | "retired" | "not_locked";

/** Where a task came from (schemas.TaskProvenance). Every field is optional. */
export interface TaskProvenance {
	/** Repository URL or path the task was cut from. */
	source?: string;
	base_commit?: string;
	reference_commit?: string;
	/** SPDX id, or "proprietary". */
	license?: string;
	/** Benchmark and version, for imported tasks. */
	imported_from?: string;
	/** Tool that scaffolded it, for example "task init". */
	created_by?: string;
}

/** A suite's base image: the toolchain every task and verifier runs on. */
export interface SuiteImage {
	tag: string;
	/** Filled by `suite lock` once the image is built. */
	digest?: string;
}

/** One difference between suite.lock and the files on disk, as re-locking would record it. */
export interface SuiteDrift {
	scope: "suite" | "task" | "probe";
	id: string;
	level: "PATCH" | "MINOR" | "MAJOR";
	action: string;
	/** Lock fields that differ, for example repo_tree_sha256. */
	fields: string[];
}

/** One probe of a suite. How it works (fault plans, canary markers) is never served. */
export interface SuiteProbe {
	id: string;
	version: string;
	layer: ProbeLayerName;
	ps2_family: PS2Family;
	axis: string;
	goal_preserving: boolean;
	safe_behavior: string;
}

/** One recorded admission check. summary keeps only the grader counts. */
export interface SuiteAdmissionCheck {
	name: string;
	control: AdmissionControl;
	want: string;
	got: string;
	passed: boolean;
	summary?: string;
}

/** One task as the suites page lists it. Hidden tests are counted, never named. */
export interface SuiteTask {
	id: string;
	version: string;
	title: string;
	project: string;
	kind: string;
	/** easy, medium or hard; "" when unset. */
	difficulty: string;
	capabilities: string[];
	tags: string[];
	provenance: TaskProvenance | null;
	status: SuiteTaskStatus;
	/** The task's files no longer match its lock entry: its admission no longer holds. */
	stale: boolean;
	/** Admitted, every check passed, and not stale. */
	admitted: boolean;
	admitted_at: string | null;
	/** The last admission's checks, in run order. */
	checks: SuiteAdmissionCheck[];
	hidden_test_files: number;
	required_tests: number;
	protected_globs: number;
	mutants: number;
	agent_timeout_s: number;
	// Scenario fields (scenario.go). Each is absent for a repository task.
	/** The task's own image instead of the suite's base image plus a repo folder. */
	environment?: TaskEnvironment;
	/** Started beside the agent, after the suite's. Served without environment, seed files or commands. */
	services?: ServiceSpec[];
	/** In run order; absent means one session from the instruction. */
	sessions?: SessionSpec[];
	/** The task's own output; absent uses the suite's. */
	output?: OutputSpec;
	/** Skill folder names, each holding a SKILL.md. */
	skills?: string[];
	/** No honest solution exists: a pass can only come from cheating. */
	impossible?: boolean;
}

/** One pack. A pack that failed to load has `error` set and its other fields empty. */
export interface SuiteSummary {
	id: string;
	version: string;
	description: string;
	environment: string;
	grader: string;
	verifier_result: VerifierResultFormat;
	verifier_timeout_s: number;
	/** Lowest passing reward; null unless the verifier reports a reward. */
	pass_threshold: number | null;
	base_image: SuiteImage;
	lock_hash: string;
	lock_state: SuiteLockState;
	locked_at: string | null;
	drift: SuiteDrift[];
	lock_error?: string;
	task_count: number;
	/** Tasks admitted whose files still match the lock. */
	admitted_count: number;
	probes: SuiteProbe[];
	tasks: SuiteTask[];
	error?: string;
	// Scenario fields (scenario.go).
	/** Leaderboard track; the server sends coding when the suite sets none. */
	track?: Track;
	/** verified, or judged: outcomes from a pinned judge model, never compared with verified suites. */
	grading?: Grading;
	/** What the verifier reads; absent means diff. */
	output?: OutputKind;
	/** Ids of the suite's services, started in every run. */
	services?: string[];
	/** Where an imported suite came from. */
	imported?: ImportRecord;
}

/** GET /suites: every pack under the benchmarks root, sorted by id. */
export interface SuiteList {
	suites: SuiteSummary[];
}

/** One visible file of a task's repo/ folder. */
export interface SuiteRepoFile {
	path: string;
	size: number;
}

/** GET /suites/{id}/tasks/{task}: the list entry plus what the agent sees. */
export interface SuiteTaskDetail {
	suite: string;
	suite_version: string;
	verifier_result: VerifierResultFormat;
	task: SuiteTask;
	/** The instruction file as text. Render it as text, never as HTML. */
	instruction: string;
	instruction_truncated: boolean;
	/** Protected globs, relative to the workspace. */
	protected: string[];
	/** Sorted by path, at most 500. */
	files: SuiteRepoFile[];
	files_total: number;
	files_truncated: boolean;
}

/** An agent config's budgets (schemas.Budgets). */
export interface AgentBudgets {
	wall_s: number;
	turns?: number;
	cpus: number;
	mem_gb: number;
	pids_limit?: number;
	usd?: number;
}

/** How the run's virtual key routes (schemas.GatewayPolicy). */
export interface AgentGateway {
	provider: string;
	max_retries: number;
	fallbacks?: string[];
}

/** One agents/*.yaml config. Env values and delivered file contents are never served. */
export interface AgentSummary {
	id: string;
	/** Relative to the bench root, for example agents/codex.yaml. */
	path: string;
	adapter: string;
	image: string;
	dockerfile: string;
	base_arg: string;
	/** build.base_arg set: built once per suite on that suite's base image. */
	per_suite_build: boolean;
	/** "" means gateway-level evidence only. */
	parser: string;
	evidence_level: "native" | "gateway";
	/** The config's default model; experiments usually set it. */
	model: string;
	scripted_kind: string;
	notes_project_file: string;
	notes_user_file: string;
	budgets: AgentBudgets;
	gateway: AgentGateway;
	/** Variable names only, sorted. */
	env_names: string[];
	/** Container paths the adapter writes, sorted. */
	file_paths: string[];
	/** Sealed runs of this config id in any experiment; null without a run index. */
	sealed_runs: number | null;
	error?: string;
}

/** GET /agents: every agents/*.yaml config, sorted by file name. */
export interface AgentList {
	agents: AgentSummary[];
}

// ---------------------------------------------------------------------------------------------
// scenario.go: tasks beyond "edit a repository and pass hidden tests" (docs/scenarios-design.md).
// Every field added to an older type for scenarios is optional here, as it is omitempty there.
// ---------------------------------------------------------------------------------------------

/** Suite tracks group suites on the leaderboard (Suite.Track). */
export type Track = "coding" | "terminal" | "tool_use" | "mcp" | "memory" | "skills" | "safety" | "qa";

/** The tracks in display order. */
export const TRACKS: readonly Track[] = ["coding", "terminal", "tool_use", "mcp", "memory", "skills", "safety", "qa"];

/** verified: a deterministic check. judged: a pinned judge model decides; never compared with verified. */
export type Grading = "verified" | "judged";

/** What the verifier reads (OutputSpec.Kind). */
export type OutputKind = "diff" | "answer" | "state";

/** A task's own container image (schemas.TaskEnvironment). */
export interface TaskEnvironment {
	image?: string;
	/** Relative to the task folder; builds the image when `image` is empty. */
	dockerfile?: string;
	context?: string;
	/** Folder inside the image that seeds the workspace volume. */
	workspace_path?: string;
	verifier_image?: string;
	/** Network during the image build only; the run never gets it. */
	build_network?: boolean;
}

/** An image to build or pull (schemas.ImageSpec). */
export interface ImageSpec {
	dockerfile?: string;
	tag: string;
	digest?: string;
	base_arg?: string;
	context?: string;
}

/** When a service is ready (schemas.ReadyCheck). */
export interface ReadyCheck {
	argv?: string[];
	http?: string;
	timeout_s?: number;
}

/** One piece of a service's final state read after the agent exits (schemas.StateExport). */
export interface StateExport {
	id: string;
	argv?: string[];
	path?: string;
	max_bytes?: number;
}

/** How an MCP service is reached (schemas.MCPEndpoint). */
export interface MCPEndpoint {
	transport?: string;
	path?: string;
}

export type ServiceRole = "data" | "tool" | "mcp" | "user";

/**
 * One container started beside the agent (schemas.ServiceSpec). The suites API serves it without
 * env, files or argv, and with export ids and paths only.
 */
export interface ServiceSpec {
	id: string;
	role?: ServiceRole;
	image?: string;
	build?: ImageSpec;
	argv?: string[];
	env?: Record<string, string>;
	files?: Record<string, string>;
	port?: number;
	ready?: ReadyCheck;
	export?: StateExport[];
	mcp?: MCPEndpoint;
	/** Routed through the per-run tool proxy; absent means true for tool and mcp services. */
	proxy?: boolean;
	/** Off the agent's network: only the verifier and other services reach it. */
	hidden?: boolean;
}

/** One agent session of a multi-session task (schemas.SessionSpec). */
export interface SessionSpec {
	id: string;
	/** Instruction file, relative to the task folder. */
	instruction: string;
	/** Folder copied into the workspace before the session starts. */
	files?: string;
	/** Keeps the previous session's workspace instead of resetting it. */
	keep_workspace?: boolean;
	wall_s?: number;
}

/** What the verifier reads (schemas.OutputSpec). */
export interface OutputSpec {
	kind?: OutputKind;
	/** Where the agent writes its answer, relative to the workspace (default answer.txt). */
	answer_path?: string;
}

/** Where an imported suite came from (schemas.ImportRecord). */
export interface ImportRecord {
	importer: string;
	/** For example terminal-bench@2.1. */
	benchmark: string;
	/** Repository or dataset URL. */
	source: string;
	revision?: string;
	/** SPDX id as upstream states it. */
	license: string;
	at: string;
}

/** The injected fault that answered instead of the tool server. */
export type ToolFaultMode = "error" | "timeout" | "poison";

/**
 * One tool invocation recorded by the per-run tool proxy (schemas.ToolCall). Arguments and results
 * are agent- and tool-written: inert text, never parsed or rendered as HTML.
 */
export interface ToolCall {
	/** 1-based order within the run. */
	seq: number;
	session?: string;
	/** Service id. */
	server: string;
	/** Tool name; for plain HTTP tool servers the method and path. */
	tool: string;
	protocol: string;
	arguments?: string;
	args_sha256: string;
	result?: string;
	result_sha256: string;
	is_error: boolean;
	/** HTTP status from the tool server. */
	status?: number;
	fault?: ToolFaultMode;
	nonce?: string;
	started: string;
	latency_ms: number;
	/** The proxy could not hand the answer to the agent (it hung up first). */
	undelivered?: boolean;
}

/** Where the exported answer came from. */
export type AnswerSource = "file" | "final_message" | "missing";

/** The agent's exported answer (schemas.Answer). Inert text. */
export interface Answer {
	text: string;
	source: AnswerSource;
	sha256: string;
	truncated?: boolean;
}

/** How one session of a multi-session run ended (schemas.SessionResult). */
export interface SessionResult {
	id: string;
	/** 1-based. */
	index: number;
	exit_code: number;
	timed_out: boolean;
	started: string;
	ended: string;
	/** agent.stream.<index>.jsonl in the run folder. */
	stdout_path: string;
}

/** One service of a run: what ran and whether it came up (schemas.ServiceResult). */
export interface ServiceResult {
	id: string;
	role?: ServiceRole;
	image: string;
	image_id?: string;
	ready: boolean;
	error?: string;
	proxied?: boolean;
}

/** One exported piece of service state in the run folder (schemas.StateFile). */
export interface StateFile {
	service: string;
	id: string;
	/** Relative to the run folder: state/<service>/<id>. */
	path: string;
	sha256: string;
	bytes: number;
	error?: string;
}

/** One importer as `bifrost-bench import list` prints it (importers.Info). */
export interface ImporterInfo {
	/** Format name, for example harbor. */
	name: string;
	/** Upstream benchmarks in this format, for example terminal-bench@2.1. */
	benchmarks: string[];
	/** Where to get the data: a clone or download command. */
	source: string;
	/** As upstream states it; "varies" when per task. */
	license: string;
	track: Track;
	grading: Grading;
	/** What a run needs beyond the default stack, in plain words. */
	needs: string[];
	summary: string;
}

/** GET /imports: every importer the server links, sorted by name. */
export interface ImportList {
	importers: ImporterInfo[];
}

export type WorkerState = "idle" | "running" | "lost";

/** One remote worker (schemas.WorkerInfo). */
export interface WorkerInfo {
	id: string;
	host: string;
	/** Bench build the worker runs. */
	version: string;
	state: WorkerState;
	/** Concurrent runs it offers. */
	slots: number;
	/** Slot ids it holds. */
	running: string[];
	completed: number;
	last_seen: string;
}

/** GET /workers. enabled false means worker mode is off (BENCH_WORKER_TOKEN unset); so does a 404 from an older server. */
export interface WorkerList {
	enabled: boolean;
	workers: WorkerInfo[];
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
export type TriageResponse = WithMode<Triage>;
export type ReleaseListResponse = WithMode<ReleaseList>;
export type ReleaseResponse = WithMode<ReleaseRecord>;
export type LeaderboardResponse = WithMode<Leaderboard>;
export type SuitesResponse = WithMode<SuiteList>;
export type SuiteTaskResponse = WithMode<SuiteTaskDetail>;
export type AgentsResponse = WithMode<AgentList>;
export type ImportsResponse = WithMode<ImportList>;
export type WorkersResponse = WithMode<WorkerList>;
