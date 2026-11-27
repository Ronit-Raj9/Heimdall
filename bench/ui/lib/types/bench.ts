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

export type TraceSource = "protocol" | "native_parser" | "generic_parser" | "stdout_tail" | "none";
export type TraceQuality = "complete" | "partial" | "degraded" | "claim_only" | "unavailable";

export interface ParseDiagnostics {
	source?: TraceSource;
	quality?: TraceQuality;
	parser?: string;
	parser_version?: string;
	bytes?: number;
	lines?: number;
	recognized?: number;
	emitted?: number;
	malformed?: number;
	oversized?: number;
	unrecognized?: number;
	truncated?: boolean;
	reason?: string;
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
	/** The strongest witness of the call; absent on runs recorded before proof classes. */
	proof_class?: ToolProofClass;
}

/** Proof class of a requested tool call: its strongest witness, one class per call (evidence/toolproof.go). result_returned proves only that a result went back to the model. */
export type ToolProofClass =
	| "proxy_witnessed"
	| "process_witnessed"
	| "file_effect_witnessed"
	| "shim_witnessed"
	| "result_returned"
	| "no_witness";

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
	agent_trace?: ParseDiagnostics;
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
	/** X1 vision grading: captures, per-check numbers and the vision judge's report (schemas/visual.go). */
	visual?: VisualGrade;
}

// X1 vision grading (schemas/visual.go, docs/visual-grading.md). Key lists:
// handlers/testdata/contract/Visual*.keys. X1 block ends after VisualJudgeResult.

/** A rectangle in image pixels; x and y are its top-left corner. */
export interface VisualRect {
	x: number;
	y: number;
	w: number;
	h: number;
}

/** One image of a visual grade in the run folder (visual/capture.png, reference.png, diff.png). */
export interface VisualImage {
	path: string;
	sha256: string;
	bytes: number;
	width: number;
	height: number;
}

/** How the bench took the capture. */
export interface VisualSource {
	kind: "browser" | "desktop" | "artifact" | string;
	service?: string;
	url?: string;
	artifact?: string;
	renderer?: string;
	width?: number;
	height?: number;
	scale?: number;
	full_page?: boolean;
	fonts_sha256?: string;
	format: string;
	pointer_parked?: boolean;
}

/** One deterministic check's number; what value means follows the method. */
export interface VisualCheckResult {
	id: string;
	method: string;
	value: number;
	threshold: number;
	passed: boolean;
	detail?: string;
	area?: VisualRect;
	found?: { x: number; y: number };
}

/** The vision judge's report. It decides only with a calibration record for its model and prompt. */
export interface VisualJudgeResult {
	model: string;
	served_model?: string;
	prompt_version: string;
	prompt_sha256: string;
	rubric_sha256: string;
	aggregate: string;
	/** pass, fail or unparsable per sample. */
	votes: string[];
	scores: number[] | null;
	score?: number;
	/** pass, fail or undecided. */
	verdict: string;
	/** The judge model's own words: untrusted text, shown clipped. */
	rationale?: string;
	usd: Value;
	price_ref?: string;
	calibration?: string;
	decides: boolean;
	reason?: string;
	agreement?: CalibrationStats;
}

/** What a visual grade rested on (Grade.visual). */
export interface VisualGrade {
	capture: VisualImage;
	source: VisualSource;
	reference?: VisualImage;
	diff?: VisualImage;
	masks?: VisualRect[];
	checks?: VisualCheckResult[];
	passed: number;
	score?: number;
	judge?: VisualJudgeResult;
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
	/** Custom judge prompt (custom-v1): each criterion's verdict and reason, the weighted share met and the share a pass needs. */
	criteria?: JudgeCriterion[];
	score?: number;
	pass_at?: number;
}

/** One criterion of a custom judge prompt's verdict. */
export interface JudgeCriterion {
	id: string;
	text: string;
	weight: number;
	met: boolean;
	reason?: string;
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
	| "image"
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
	parser_version?: string;
	trace_source?: TraceSource;
	trace_quality?: TraceQuality;
	trace_diagnostics?: ParseDiagnostics;
	config_hash: string;
	exit_code?: number;
	timed_out: boolean;
	/** The out-of-memory killer ended the agent: an infrastructure error, excluded from scores. */
	oom_killed?: boolean;
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
	/** X7: the GPUs a GPU task's run held (schemas/gpu.go); absent for every other run. */
	hardware?: HardwareRecord;
	/** X20b: a replay, fork or breakpoint run's link to the run it came from (lib/types/runDebug.ts). */
	debug?: RunDebugInfo;
	/** Z03: a person took control of this debug run; it never counts (schemas/recordings.go). */
	human_assisted?: boolean;
}

/** run.json debug (schemas/debug.go): where a debug run came from. lib/types/runDebug.ts re-exports it as RunDebug. */
export interface RunDebugInfo {
	mode: "replay" | "fork" | "step";
	from_run?: string;
	from_exp?: string;
	call?: number;
	held_ms?: number;
	/** The source run's fault condition when it was not the clean one. */
	from_condition?: string;
	edit?: "message" | "response";
}

// X7: GPU benchmarking (schemas/gpu.go, docs/gpu-tasks.md).
export interface GPUInfo {
	index: number;
	uuid: string;
	name: string;
	memory_mb: number;
	driver: string;
	cuda_version?: string;
	compute_cap: string;
}

export interface GPUSamplerSummary {
	samples: number;
	interval_ms: number;
	agent_seconds: number;
	gpu_seconds: number;
	baseline_mb: number;
	peak_used_mb: number;
	peak_vram_mb: number;
	mean_util_pct: number;
	sessions: number;
	error?: string;
}

export interface HardwareRecord {
	gpus?: GPUInfo[];
	/** cdi | device_requests */
	mode?: string;
	devices?: string[];
	sampler?: GPUSamplerSummary;
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

/** Probe layers of the Go catalog (schemas/probe.go, schemas/scenario.go). A newer server may send others. */
export type ProbeLayerName = "fixture" | "gateway" | "tool_shim" | "adapter" | "a2a" | "tool" | "service";

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
	/** Which tasks `tasks: all` means: public (the default) or held_out. An explicit id list is taken as written. */
	split?: TaskSplit;
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
	/** Release policy file, relative to the bench root. */
	policy?: string;
	requirements?: CapabilityRequirements;
	smoke?: boolean;
	scripted_upstream?: boolean;
	/** Lets the suite's model judges call their real provider in a smoke or scripted_upstream experiment. */
	paid_judge?: boolean;
	/** live | record | replay: overrides every external API's mode for this experiment's runs. */
	external?: string;
	/** offline | replay | live-allowlisted: overrides every egress service's mode (recorded internet). */
	egress?: string;
	fairness?: string;
	attribution?: Record<string, unknown>;
	matrix?: Record<string, unknown>;
	simulator?: Record<string, unknown>;
	tier?: string;
	trials?: Record<string, unknown>;
	extends?: Record<string, unknown>;
	/** Hosted mode: the runner pool of the slots; absent runs on the default pool. */
	runner?: { pool?: string };
	aa?: boolean;
	/** standard | neutral */
	realism?: string;
	safety?: string;
	codescan?: Record<string, unknown>;
	gateway_test?: { feature: string; control: string; variants: string[]; options?: Record<string, string> };
	environment?: Record<string, unknown>;
	/** The evaluation that created this experiment, when one did. */
	evaluation_id?: string;
	/** Twin arms of a base arm that differ only in a framing preamble (docs/control-evals.md). */
	framing_pairs?: Record<string, unknown>[];
	/** Overrides of the retry rules of the failure classes that allow it. */
	error_policy?: Record<string, unknown>;
	/** The experiment whose failed tasks this one runs again ("Rerun failures"): exploratory, never counted. */
	rerun_of?: string;
	/** A debug run of one recorded run: replay, fork or breakpoints. */
	debug?: Record<string, unknown>;
	/** Runs chained over the tasks, with memory carried from one run to the next when memory is carry. */
	stream?: { memory: "carry" | "fresh"; order?: "suite" | "seeded"; fresh_arm?: string; replay?: number; threshold?: number };
}

// ---------------------------------------------------------------------------------------------
// verdict.go: verdict and analysis (DESIGN.md 9.3, 9.4, 9.6)
// ---------------------------------------------------------------------------------------------

/** score: a one-setup experiment, scored on its own with no comparison (Verdict.score). */
export type Decision = "improved" | "regressed" | "inconclusive" | "invalid" | "score" | "leader";

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
	/** Sign-flip, two-sided; absent when descriptive. */
	p_value?: number;
	/** Monte Carlo sign vectors behind p_value; absent when it is exact. */
	p_draws?: number;
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
	basis?: "effect" | "guard";
	setups?: SetupsVerdict;
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
	invalid_scope?: "runs" | "experiment";
	/** Measured instrument checks; a failed one is also an invalid reason. Absent on an audit verdict. */
	instruments?: InstrumentCheck[];
	/** "unknown" or "" on an audit verdict: a published table says nothing about veto gates. */
	integrity: IntegrityStatus | "";
	manifest_hash: string;
	frozen_at: string;
	/** Eval integrity area (S2): the noise floor behind the MDE. Absent on analyses written before it. */
	variance?: VarianceCard;
	/** Set (and decision is score or invalid) when the experiment has one setup: its score with an interval, no comparison. */
	score?: Score;
}

export interface SetupsVerdict {
	leader: string;
	top_group: string[];
	behind: string[];
	separable: boolean;
	note: string;
	pairs: MatrixPair[];
	safety?: Record<string, string[]>;
}

/** schemas.Score: the primary metric of a one-setup experiment's only arm. Rates are fractions (0.62 = 62%). */
export interface Score {
	arm: string;
	label?: string;
	condition: string;
	metric: string;
	/** Over observed runs; absent when none was observed. */
	mean?: number;
	/** conf interval of mean; absent when it cannot be computed. */
	interval?: [number, number];
	conf: number;
	method: string;
	/** Every unknown run at its lowest possible value. */
	low?: number;
	/** Every unknown run at its highest possible value. */
	high?: number;
	tasks: number;
	runs: number;
	observed: number;
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
	/** Counted runs whose outcome came from a human review (grade source reviewed); absent otherwise. */
	reviewed_runs?: number;
	/** Of reviewed_runs, those whose outcome differs from the sealed one. */
	review_changed_runs?: number;
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
	/** The human review this run's outcome came from, under grade source reviewed only. */
	review?: Review;
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
	/** Probe id to layer for this suite; catalog.probes is flattened across suites and ids can repeat. */
	probe_layers?: Record<string, ProbeLayerName>;
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
	partial?: boolean;
	analysis_id: string;
	grade_source: string;
	suite: string;
	verdict: Verdict;
	naive: Record<string, Ratio>;
	gates: GateSummary[];
	config_diff: ConfigChange[];
	/** The release policy result; absent when the experiment has no policy. */
	policy?: PolicyResult;
	/** Counted runs whose outcome came from a human review (grade source reviewed); absent otherwise. */
	reviewed_runs?: number;
	/** Of reviewed_runs, those whose outcome differs from the sealed one. */
	review_changed_runs?: number;
	/** W8 judge calibration: the judge_calibration instrument of a judged experiment; absent otherwise. */
	judge_calibration?: JudgeCalibrationVerdict;
	/**
	 * M8-68: the experiment status a shared verdict shows (cost, date, sample size, who ran it). Sent
	 * by the share link (KindVerdict) once the server adds them; absent from an older server.
	 */
	spent_usd?: number;
	finished_at?: string;
	created_by?: string;
	runs_total?: number;
	runs_counted?: number;
	tasks?: number;
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
	/** The rule set's file name without .yaml, as Releases > Rules lists it. */ policy_file?: string;
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
	/** The arm's own provenance (scripted control, smoke or scripted ledger), independent of the server mode. */
	synthetic: boolean;
	/** This run's own model evidence. Only "model" means a real model is recorded to have played it. */
	provenance: RunProvenance;
	/** Why provenance is unavailable. */
	provenance_note?: string;
}

/** A pair run's model evidence (leaderboard.Provenance*). */
export type RunProvenance = "scripted" | "model" | "mixed" | "unobserved" | "unavailable" | "replayed";

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
	/** The experiment's suite id, so the task links to its evidence. */
	suite?: string;
	/** The suite version the experiment froze, so a task link opens that exact revision. */
	suite_version?: string;
	condition: string;
	trial: number;
	runs: PairRun[];
	alignment: AlignedStep[];
	first_divergence: FirstDivergence | null;
}

/** GET /runs/{id}: one run with its seal, grade, metrics, gates and receipts. Unwritten files are null or empty. */
export interface RunDetail {
	adapter_fault?: AdapterFaultEvidence;
	slot: Slot;
	run: Run;
	sealed: boolean;
	/** Empty until the run is sealed. */
	seal_hash: string;
	debug_allowed?: { replay: boolean; fork: boolean; reason?: string; breakpoints?: boolean; breakpoints_reason?: string };
	/** Run control: requeued by an operator; kept, never counted. */
	superseded?: boolean;
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
	/** The run's output kind (diff, answer, state, artifact); absent on older servers. */
	output?: OutputKind;
	/** Services that ran beside the agent. */
	services?: ServiceResult[];
	/** Files an artifact task's agent produced, copied into the sealed run folder. */
	artifacts?: ArtifactFile[];
	evidence_contract?: RunEvidenceContract;
	canonical?: CanonicalRun;
}

export interface AdapterFaultEvidence {
	version: string;
	run_id: string;
	rule_hash: string;
	complete: boolean;
	reason?: string;
	ledger_hash: string;
	entries: AdapterFaultEntry[];
}
export interface AdapterFaultEntry {
	seq: number;
	at: string;
	run_id: string;
	session: string;
	operation_id?: string;
	kind?: string;
	name?: string;
	phase: string;
	mode?: string;
	success?: boolean;
	prev_hash: string;
	hash: string;
	message?: string;
}

/** GET /runs/{id}/events */
export interface RunEvents {
	run_id: string;
	/** False when the gateway could not return every request row. Shown, never hidden. */
	complete: boolean;
	events: BenchEvent[];
}

// Agent step timeline (schemas/steps.go, handlers/runsteps.go).
/** The independent witness a timeline row came from. */
export type StepSource =
	| "gateway"
	| "tool_proxy"
	| "process_observer"
	| "workspace_observer"
	| "agent_stream"
	| "shim"
	| "network_observer"
	| "agent_reported";
export type StepKind =
	| "model_turn"
	| "model_attempt"
	| "tool_request"
	| "tool_execution"
	| "command"
	| "file_change"
	| "network"
	| "agent_event";
/** How a requested tool call is known to have run. */
export type StepExecution = "executed_with_proof" | "executed_agent_reported" | "no_witness";

/**
 * One row of the unified step timeline: one observation by one witness, in time order. Every
 * string (summary, path, tool_name, conflict) is agent-influenced: render as inert text only.
 */
export interface StepRecord {
	seq: number;
	ts: string;
	kind: StepKind;
	source: StepSource;
	proof: EvidenceStrength;
	/** The logical model step whose response came before this row; 0 before the first response. */
	turn: number;
	session?: number;
	summary: string;
	request_id?: string;
	attempt?: number;
	tool_call_id?: string;
	tool_name?: string;
	pid?: number;
	ppid?: number;
	path?: string;
	op?: string;
	exit_code?: number;
	duration_ms?: number;
	is_error?: boolean;
	/** tool_request rows only. */
	execution?: StepExecution;
	/** tool_request rows: the strongest witness of the call (docs/steps-and-evidence.md, proof class); trusted classes are proxy, process and file effect. */
	proof_class?: ToolProofClass;
	/** tool_request rows: seq of the rows that witness its execution. */
	witnessed_by?: number[];
	/** seq of the counted row this row witnesses. */
	witness_of?: number;
	/** The row counts toward agent_steps (every witnessed action, each once), not toward steps (model turns answered). */
	counted?: boolean;
	/** Witnesses disagree about this row. */
	conflict?: string;
	// Agent-reported rows (source agent_reported, schemas/agentevents.go): the agent's own word,
	// the weakest witness. event_type and report_id are agent-chosen display text.
	/** The type the agent reported: plan, tool_call, subagent_start, ... Unknown values are shown as text. */
	event_type?: string;
	/** How the report arrived. */
	channel?: StepReportChannel;
	/** The agent's own id for the event. */
	report_id?: string;
	/** seq of the reported parent row: nesting of subagent spans and handoffs. */
	parent_seq?: number;
	/** Whether a trusted witness saw the reported action. */
	corroboration?: StepCorroboration;
}

/** Channels an agent-reported row arrived on. Other values may appear and are shown as text. */
export type StepReportChannel = "otlp" | "native" | "file" | "stdout" | (string & {});
/** Whether a trusted witness backs an agent-reported row. */
export type StepCorroboration = "corroborated" | "agent_reported_only" | "conflicts";

/**
 * What a run's agent reported about itself and what was kept (schemas/agentevents.go,
 * evidence.json agent_events). collector is "on", "off" or "missing"; dropped counts events refused
 * at a cap or because they did not parse; received counts every event seen, dropped ones included.
 */
export interface AgentEventStatus {
	collector: "on" | "off" | "missing";
	reason?: string;
	received: number;
	kept: number;
	dropped?: number;
	/** Kept events per channel. */
	channels?: Record<string, number>;
	/** Kept events per reported type (agent-chosen keys: display text only). */
	kinds?: Record<string, number>;
}

/** Whether one trusted observer ran for one session. Missing is evidence with a reason, never zero activity. */
export interface ObserverStatus {
	observer: "process" | "workspace";
	session?: number;
	state: "observed" | "off" | "missing";
	reason?: string;
	mode?: string;
	interval_ms?: number;
	observer_coverage?: string;
	records: number;
	dropped?: number;
	pids_allocated?: number;
	pids_seen?: number;
}

/** GET /runs/{id}/steps. available is false for a run recorded before the timeline; lists are then empty. */
export interface RunSteps {
	run_id: string;
	available: boolean;
	steps: StepRecord[];
	observers: ObserverStatus[];
	/** The timeline was longer than one response carries; the first rows are sent. */
	truncated?: boolean;
	/** Lines of steps.jsonl that did not parse. */
	skipped_lines?: number;
	/** What the agent reported about itself (docs/agent-events.md); absent when no collector ran and nothing was reported. */
	agent_events?: AgentEventStatus;
}

export type RunStepsResponse = WithMode<RunSteps>;

/** State of one remote coverage channel: on, off, or missing (on in config, but nothing arrived). */
export type RemoteChannelState = "on" | "off" | "missing";

/** Which channels of a remote agent's run were active (schemas/remote.go). */
export interface RemoteChannels {
	model_traffic: RemoteChannelState;
	tools: RemoteChannelState;
	self_report: RemoteChannelState;
}

/** What a remote agent said about itself in its answer. Every field is the agent's own word: inert display text, never trusted. */
export interface RemoteReport {
	input_tokens?: number;
	output_tokens?: number;
	total_tokens?: number;
	cost_usd?: number;
	model?: string;
	/** Self-declared. */
	version?: string;
	/** Self-declared. */
	build?: string;
	events?: number;
	events_dropped?: number;
	artifacts?: string[];
	async?: boolean;
	notes?: string[];
}

/** A remote agent's channel summary: what is trusted, agent-reported or unknown. */
export interface RemoteEvidence {
	channels: RemoteChannels;
	trusted?: string[];
	agent_reported?: string[];
	unknown?: string[];
	reported?: RemoteReport;
}

/** GET /runs/{id}/remote. available is false (remote null) for a run of an agent that is not remote. */
export interface RunRemote {
	run_id: string;
	available: boolean;
	remote: RemoteEvidence | null;
}

export type RunRemoteResponse = WithMode<RunRemote>;

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
	// U2 UI regroup: the manifest's suite and arm ids; on GET /experiments only, the newest analysis's decision.
	suite?: string;
	/** The suite version the manifest froze, so task links open that revision. */
	suite_version?: string;
	arms?: string[];
	/** A Decision (improved, regressed, inconclusive, invalid); typed as text so a newer server's value still renders. */
	decision?: string;
	evaluation_id?: string;
	debug?: string;
	debug_from_run?: string;
	multi_agent?: boolean;
	setups?: { arm: string; agent: string; agent_file: string; model: string }[];
	problem?: string;
	run_job_id?: string;
	arms_differ?: "model" | "none" | "agent";
	paused?: boolean;
	paused_models?: string[];
	extends?: string;
	latest_regrade?: RegradeRef; // the newest committed re-score, on GET /experiments/{id} only
}

/** One committed re-score of an experiment (ExperimentStatus.latest_regrade). */
export interface RegradeRef {
	id: string;
	created_at: string;
	note?: string;
}

/** GET /experiments */
export interface ExperimentList {
	experiments: ExperimentStatus[];
}

// U2 UI regroup: GET /runs, every run across experiments (handlers/runlist.go).
/** One run of GET /runs. track and output come from the suite and are absent when it could not be loaded. */
export interface RunListRow {
	run_id: string;
	exp_id: string;
	pair_id: string;
	suite: string;
	track?: string;
	output?: OutputKind;
	/** verified | judged: who decided y. */
	grading?: string;
	task: string;
	arm: string;
	condition: string;
	trial: number;
	attempt: number;
	counted: boolean;
	sealed: boolean;
	outcome: Outcome;
	y: Value;
	/** Read from metrics.json; absent until the run has one. */
	cost_usd?: Value;
	config_id: string;
	started?: string;
	sealed_at?: string;
	/** The first part failure's error of a non-pass run; absent when it recorded none. Inert text. */
	error?: string;
	/**
	 * M2-42: why a non-pass run failed in plain words (the grader's verdict reason, or "cheating
	 * detected"); absent until the server sends it, then the list falls back to error. Inert text.
	 */
	fail_reason?: string;
	paired?: boolean;
	/** Why a sealed run is left out of scores: out_of_memory, infra_error, invalid_evaluator, unknown. */
	excluded?: string;
	/** Run metrics from metrics.json; absent until observed, and the list shows a dash. */
	model?: string;
	steps?: number;
	tool_calls?: number;
	tokens?: number;
	/** The agent's own version string, when it declares one. */
	agent_version?: string;
	/** The registered agent version (v1, v2) whose config matches; absent when none. */
	version?: number;
	/** The fault injected in this run (model-down, broken-tool, misleading-instruction, ...); absent for a clean run. */
	fault?: string;
}

/** The distinct values of each run list filter, across every run. */
export interface RunListFacets {
	experiments: string[];
	suites: string[];
	arms: string[];
	tasks: string[];
	conditions: string[];
	outcomes: string[];
	tracks: string[];
	/** Agent names (config ids) seen across every run. */
	agents: string[];
	/** Fault names seen across every run; "clean" for runs without one. */
	faults: string[];
	models: string[];
}

/** GET /runs: one page, newest first; total counts every run that matched. */
export interface RunList {
	runs: RunListRow[];
	total: number;
	limit: number;
	offset: number;
	facets: RunListFacets;
}

/** The filters and page of GET /runs. An empty value is left out. */
export interface RunListParams {
	exp?: string;
	suite?: string;
	arm?: string;
	task?: string;
	condition?: string;
	outcome?: string;
	failed?: boolean;
	track?: string;
	/** Agent config id; matched on the server across every page. */
	agent?: string;
	/** Suite id with or without its version. */
	benchmark?: string;
	/** Fault name, or "clean". */
	fault?: string;
	model?: string;
	limit?: number;
	offset?: number;
	sort?: "started" | "duration" | "outcome" | "cost" | "tokens" | "steps" | "task" | "agent";
	dir?: "asc" | "desc";
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
	/** False on an offline (replay-only) server: planning and starting are unavailable. */
	launch?: boolean;
}

/** Whether the server launches experiments; unknown (older servers, hosted root) counts as yes. */
export function launchAvailable(health: Health | undefined): boolean {
	return health?.launch !== false;
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

/** POST /experiments/{exp_id}/regrade: the experiment in the path, the optional rest in the body. */
export interface RegradeRequest extends ExperimentRegradeRequest {
	exp_id: string;
}

/** schemas.RegradeResult: a regrade as the CLI reports it. The API answers with the regrade job. */
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
	/** The agent this release put live, the version, and the version it replaced (absent or 0: none was live). */
	agent_id?: string;
	version?: number;
	replaces_version?: number;
	/** Set when the release covers a whole evaluation; exp_id then names its first benchmark. */
	evaluation_id?: string;
	children?: ReleaseChild[];
	summary?: ReleaseSummary;
	/** The evaluation's gate at release time: pass, fail or no_baseline. */
	gate?: string;
}

/** One benchmark of a release that covers an evaluation. */
export interface ReleaseChild {
	benchmark: string;
	exp_id: string;
	analysis_id: string;
	decision: Decision;
	action: Action;
	policy_name?: string;
	policy_decision?: ReleasePolicyDecision;
	manifest_hash: string;
	seals_digest: string;
	runs: number;
}

/** A score with its 95% interval and run count. */
export interface ScoreCI {
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
}

/** One agent version's numbers in a release summary; absent fields are unknown. */
export interface ReleaseSide extends ScoreCI {
	label: string;
	agent: string;
	version?: number;
	model?: string;
	evaluation_id?: string;
	cost_per_solved_usd?: number;
	/** Share of failed runs the agent claimed as solved. */
	false_completion?: number;
	/** Success rate on runs with a fault injected. */
	fault_survival?: number;
}

export interface ReleaseCapabilityScore {
	capability: string;
	candidate: ScoreCI;
	baseline?: ScoreCI;
}

/** Before (baseline) and after (candidate) of a release. */
/** Whether a baseline from another evaluation ran the same benchmark version and tasks. */
export interface DatasetComparison {
	changed: boolean;
	benchmarks: DatasetBenchmark[];
	summary: string;
}

export interface DatasetBenchmark {
	benchmark: string;
	candidate_version?: string;
	baseline_version?: string;
	candidate_lock?: string;
	baseline_lock?: string;
	changed: boolean;
	/** Tasks both sides ran with the same content. */
	compared: number;
	new_tasks: number;
	dropped_tasks: number;
	not_compared?: boolean;
}

export interface ReleaseSummary {
	candidate: ReleaseSide;
	baseline?: ReleaseSide;
	capabilities?: ReleaseCapabilityScore[];
	note?: string;
	/** A baseline from another evaluation: whether the benchmark changed since (scores then use the shared tasks only). */
	dataset?: DatasetComparison;
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

/**
 * original: every run as sealed. latest: each experiment's newest regrade, where it has a result for the run.
 * reviewed: the sealed grades with human reviews applied on top (never the regrades).
 */
export type LeaderboardGradeSourceMode = "original" | "latest" | "reviewed";

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
	tasks_solved?: number;
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
	/** Of runs, those whose outcome came from a human review (grade source reviewed). */
	reviewed_runs?: number;
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
	/** Runs on the board whose outcome came from a human review (grade source reviewed); absent otherwise. */
	reviewed_runs?: number;
	/** Of reviewed_runs, those whose outcome the review changed. */
	review_changed_runs?: number;
	/** held_out on the held-out board (runs of held-out tasks only); absent on the public board, which never counts a held-out task. */
	split?: TaskSplit;
	/** How the board's runs were graded, from the frozen plan and sealed grade.json: verified, judged, mixed or unknown. */
	grading: "verified" | "judged" | "mixed" | "unknown";
	/** Judge models of judged runs. */
	judges?: string[];
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
	/** held_out asks for the held-out board; absent is the public board. */
	split?: "held_out";
}

// ---------------------------------------------------------------------------------------------
// review.go: human review. A person's decision about one sealed run, kept as a hash-chained child
// artifact beside the run; it never edits sealed evidence and applies only under grade source reviewed.
// ---------------------------------------------------------------------------------------------

/** pass, fail and invalid replace the outcome under grade source reviewed; flag_only changes nothing. */
export type ReviewDecision = "pass" | "fail" | "invalid" | "flag_only";

/** Every review decision, in form order. */
export const REVIEW_DECISIONS: readonly ReviewDecision[] = ["pass", "fail", "invalid", "flag_only"];

/** Why a run is in the review queue. */
export type ReviewReason =
	| "split_votes"
	| "unparsable_votes"
	| "invalid_evaluator"
	| "veto"
	| "false_completion"
	| "flagged"
	| "contested"
	| "invalidated"; // W8 judge calibration: contested; fix wave 3: invalidated

/** Every queue reason, in display order (schemas.ReviewReasons). */
export const REVIEW_REASONS: readonly ReviewReason[] = [
	"split_votes",
	"unparsable_votes",
	"invalid_evaluator",
	"veto",
	"false_completion",
	"flagged",
	"contested", // W8 judge calibration
	"invalidated", // fix wave 3: a safety check threw the run out
];

/** One review record (schemas.Review): <exp>/reviews/<review_id>/review.json. */
export interface Review {
	review_id: string;
	exp_id: string;
	run_id: string;
	/** The run's seal hash when the review was written. */
	parent_seal: string;
	decision: ReviewDecision;
	/** Plain text. Inert. */
	reason: string;
	/** Self-declared name; there is no authentication. */
	reviewer: string;
	created_at: string;
	sealed_outcome: Outcome;
	/** The outcome in effect before this review: the sealed one or the superseded review's. */
	previous_outcome: Outcome;
	/** The earlier decision review of the run this one replaces. */
	supersedes?: string;
	/** What the reviewer cited: files, gate names, judge votes, free text. */
	evidence?: string[];
	/** Hash on the previous reviews.log line, "" for the first. */
	prev_hash: string;
	hash: string;
}

/** POST /reviews body. */
export interface ReviewRequest {
	exp_id?: string;
	run_id: string;
	decision: ReviewDecision;
	reason: string;
	reviewer: string;
	evidence?: string[];
}

/** One run the queue asks a person to look at. */
export interface ReviewQueueItem {
	run_id: string;
	exp_id: string;
	pair_id: string;
	task: string;
	arm: string;
	condition: string;
	trial: number;
	reasons: string[];
	sealed_outcome: Outcome;
	/** The newest valid decision review's outcome, else the sealed one. */
	outcome: Outcome;
	/** Valid reviews of the run, flags included. */
	reviews: number;
	/** The newest valid decision review's decision; absent when none. */
	decision?: ReviewDecision;
}

/** GET /experiments/{id}/reviews/queue */
export interface ReviewQueue {
	exp_id: string;
	items: ReviewQueueItem[];
	/** Every queue reason, for the filter. */
	reasons: string[];
	/** Reviews ignored because they do not verify, runs whose seal does not verify. */
	problems: string[];
}

/** GET /runs/{id}/reviews: every valid review in log order, oldest first, and the one in effect. */
export interface RunReviews {
	run_id: string;
	exp_id: string;
	sealed_outcome: Outcome;
	/** The outcome under grade source reviewed. */
	outcome: Outcome;
	/** The newest valid decision review; absent when none. */
	current?: Review;
	reviews: Review[];
	/** Queue reasons that apply to the run. */
	reasons: string[];
	/** Reviews of this run that were ignored, and why. */
	problems: string[];
}

/** GET /experiments/{id}/reviews: the whole chain and whether it verifies. */
export interface ReviewLog {
	exp_id: string;
	reviews: Review[];
	verified: boolean;
	problems: string[];
}

// ---------------------------------------------------------------------------------------------
// trends (bench/trends/types.go): time series per agent config lineage, read from the derived
// results index. A monitoring signal only: no verdict, leaderboard or release reads it.
// ---------------------------------------------------------------------------------------------

/** Chartable trend metric ids, the leaderboard's ids. */
export type TrendMetric = "verified_success" | "cost_per_success" | "tokens" | "wall_time_s" | "false_completion";

/** exclude: live runs only (default). include: live and synthetic. only: synthetic runs only. */
export type TrendSynthetic = "exclude" | "include" | "only";

/** A point against the previous point of its lineage, by the exact sign-flip test on common tasks. */
export type TrendStatus = "regressed" | "improved" | "no_change" | "cannot_tell" | "first";

/** The exact sign-flip test of a point's per-task success rates against the previous point's (trends.Regression). */
export interface TrendRegression {
	status: TrendStatus;
	/** Two-sided p; null when not tested. */
	p: number | null;
	/** Tasks both points ran. */
	common_tasks: number;
	/** Common tasks whose success rate changed. */
	discordant: number;
	/** Mean per-task change in success, newer minus older; null when not tested. */
	delta: number | null;
	/** Experiment of the previous point; "" for the first. */
	against: string;
	/** Plain words, for example "p 0.03 on 9 common tasks". */
	reason: string;
}

/** One config hash of a lineage in one experiment (trends.Point). Numbers are null when no run observed them, never zero. */
export interface TrendPoint {
	experiment: string;
	/** The experiment's creation time. */
	date: string;
	/** id@version. */
	suite: string;
	arm: string;
	config_hash: string;
	/** The config hash differs from the previous point's. */
	config_changed: boolean;
	synthetic: boolean;
	/** Distinct tasks with a scored run. */
	tasks: number;
	/** Scored runs. */
	runs: number;
	/** The charted metric (Trends.metric). */
	value: number | null;
	success: number | null;
	/** Descriptive 95% t interval over task rates; null under 2 tasks. */
	success_low: number | null;
	success_high: number | null;
	cost_per_success: number | null;
	/** Median per run. */
	tokens: number | null;
	/** Median per run, seconds. */
	wall_time_s: number | null;
	/** Share of runs whose claim and outcome are known. */
	false_completion: number | null;
	/** Newest analysis verdict decision; "" with no analysis. */
	decision: string;
	/** Newest analysis policy decision; "" with none. */
	policy: string;
	/** regressed: the release verdict was REGRESSED. blocked: the release policy blocked it. "" otherwise. */
	release: "" | "regressed" | "blocked";
	regression: TrendRegression;
}

/** Every config of one agent id and model, across experiments (trends.Lineage). */
export interface TrendLineage {
	/** "<agent>|<model>". */
	key: string;
	agent: string;
	/** "" when unknown. */
	model: string;
	synthetic: boolean;
	/** Ordered by experiment date. */
	points: TrendPoint[];
	/** The newest point against the one before it. */
	regression: TrendRegression;
}

/** GET /trends: points grouped into lineages, one per (agent config id, model). */
export interface Trends {
	/** id@version filter, "" for every suite. */
	suite: string;
	/** Agent config id filter, "" for every agent. */
	agent: string;
	metric: TrendMetric;
	condition: string;
	synthetic: TrendSynthetic;
	/** Significance level of the regression flag. */
	alpha: number;
	metrics: TrendMetric[];
	suites: string[];
	agents: string[];
	conditions: string[];
	/** Ordered by agent then model. */
	lineages: TrendLineage[];
	/** Points over every lineage; the page shows an empty state below 2. */
	points: number;
	/** Runs the index lists as rejected (seal failed); never charted. */
	rejected: number;
	/** Indexed runs of an experiment whose newest verdict is INVALID, or with evidence problems (e.g. a slot with two counted runs); never charted. */
	invalid: number;
	/** When the index was last written. */
	indexed_at: string;
	generated_at: string;
}

/** Query of GET /trends. An absent field takes the server default: every suite, every agent, verified_success, clean, exclude. */
export interface TrendsParams {
	suite?: string;
	agent?: string;
	metric?: TrendMetric;
	condition?: string;
	synthetic?: TrendSynthetic;
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
	/** First workflow: false while a tool-drafted instruction (tasks mine) awaits human review; `task review` sets true. */
	instruction_reviewed?: boolean;
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

/** fingerprinted: suite.lock binds every evaluator setting; legacy: it has no fingerprints section. */
export type LockKind = "fingerprinted" | "legacy";

/** One difference between the fingerprints suite.lock recorded and the files on disk. Planning refuses while any exists. */
export interface FingerprintDrift {
	scope: "suite" | "task" | "probe";
	id: string;
	/** settings (a suite.yaml, task.yaml or probe setting), files (a file they name) or image (an image id). */
	part: "settings" | "files" | "image";
	/** The settings keys or image roles that differ; absent for files. */
	fields?: string[];
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
	// Suite health fields (suitehealth.go).
	/** held_out for a held-out task; absent for a public one. */
	split?: TaskSplit;
	/** A held-out task served by id only: title, files and checks are withheld. */
	redacted?: boolean;
	/** Set while the task is kept out of new plans. */
	quarantine?: TaskQuarantine;
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
	ready_task_ids?: string[] | null;
	ready_known?: boolean;
	partial?: boolean;
	probes: SuiteProbe[];
	tasks: SuiteTask[];
	error?: string;
	/** Served from the server's read-only built-in library (HEIMDALL_LIBRARY_DIR). */
	builtin?: boolean;
	/** "library" for a built-in pack; absent for the org's own. */
	source?: "library";
	// Scenario fields (scenario.go).
	/** Leaderboard track; the server sends coding when the suite sets none. */
	track?: Track;
	/** verified, or judged: outcomes from a pinned judge model, never compared with verified suites. */
	grading?: Grading;
	/** What the verifier reads; absent means diff. */
	output?: OutputKind;
	/** The suite's declared output artifacts (output kind artifact); a task without its own list uses these. */
	artifacts?: ArtifactSpec[];
	/** Ids of the suite's services, started in every run. */
	services?: string[];
	/** Where an imported suite came from. */
	imported?: ImportRecord;
	// Suite health fields (suitehealth.go). Held-out tasks are listed by id with their counts only
	// unless the server config sets held_out.visible.
	/** Tasks with split held_out. */
	held_out_count?: number;
	/** Tasks in suite.health.json, kept out of new plans. */
	quarantined_count?: number;
	/** suite.yaml sets a canary. */
	has_canary?: boolean;
	// Lock fingerprint fields (docs/lock-fingerprints.md).
	/** Absent when the suite is unlocked. */
	lock_kind?: LockKind;
	/** What no longer matches the recorded fingerprints; absent when nothing drifted or the lock is legacy. */
	fingerprint_drift?: FingerprintDrift[];
	/** U3: the suite's tiers (docs/suite-tiers.md): pr, nightly, headroom first; nightly always listed. */
	tiers?: SuiteTier[];
	/** A judged suite's pinned judge and the judge calls its readiness check makes. */
	judge?: SuiteJudge;
}

/** A judged suite's judge on the suites API. */
export interface SuiteJudge {
	provider: string;
	model: string;
	/** Calls per judged grade (votes, doubled with an auditor). */
	votes: number;
	/** About how many judge calls the readiness check of every task makes. */
	readiness_calls: number;
}

/** U3: one tier of a suite, a named task subset an experiment picks with `tier:` (handlers/suitetiers.go). */
export interface SuiteTier {
	name: string;
	/** Task ids in suite order. */
	tasks: string[];
	/** False for nightly when suite.yaml leaves it out: every task. */
	defined: boolean;
	/** Why planning would refuse the tier, for example an unknown task. */
	error?: string;
}

/** GET /suites: every pack under the benchmarks root, sorted by id. */
export interface SuiteList {
	suites: SuiteSummary[];
	reserved_ids?: string[];
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

// ---------------------------------------------------------------------------------------------
// suitehealth.go: GET /suites/{id}/health, read from sealed runs across experiments. Verifier
// health is about the grader; outcome variance is about the agents. They are kept apart: a config
// that sometimes passes and sometimes fails is ordinary agent variance, while a verifier that
// grades the same output differently corrupts verdicts.

/** A task's split. An absent split is public. */
export type TaskSplit = "public" | "held_out";

/** Who quarantined a task: `suite quarantine` (manual) or `suite health -auto-quarantine` (auto). */
export type QuarantineSource = "manual" | "auto";

/** Why a task is kept out of new plans (schemas.TaskQuarantine). */
export interface TaskQuarantine {
	reason: string;
	at: string;
	source: QuarantineSource;
	/** The task version quarantined. */
	task_version?: string;
	/** Verifier evidence lines behind an auto quarantine. */
	evidence?: string[];
}

/** Task health flags (TaskHealth.flags). */
export type HealthFlag =
	| "flaky_verifier"
	| "saturated"
	| "dead"
	| "no_discrimination"
	| "difficulty_mismatch"
	| "infra_trouble"
	| "timeouts"
	| "insufficient_data";

/** Kinds of verifier evidence (VerifierEvidence.kind). */
export type VerifierEvidenceKind = "regrade_disagrees" | "same_output_differs" | "admission_reference" | "oracle_failed";

/** The cut-offs a health report applied, so a reader can see what "hard" or "infra trouble" meant. */
export interface HealthThresholds {
	/** Scored non-synthetic runs before difficulty is labelled. */
	min_runs: number;
	/** Distinct agent configs before difficulty is labelled. */
	min_configs: number;
	/** Pass rate at or above: easy. */
	easy_above: number;
	/** Pass rate at or below: hard. */
	hard_below: number;
	/** Infra share above: infra_trouble. */
	infra_rate: number;
	/** Timeout share above: timeouts. */
	timeout_rate: number;
	/** Confidence of the Wilson interval, for example 0.95. */
	interval_level: number;
}

/** One observation of the verifier disagreeing with itself or rejecting the reference. */
export interface VerifierEvidence {
	kind: VerifierEvidenceKind;
	detail: string;
	exp_id?: string;
	run_ids?: string[];
}

/** Evidence that the verifier, not the agent, is unstable. checks counts the comparisons that could have shown it. */
export interface VerifierHealth {
	flaky: boolean;
	checks: number;
	evidence: VerifierEvidence[];
}

/** One agent config's record on one task, non-synthetic runs only. */
export interface ConfigOutcome {
	/** Agent config id. */
	config: string;
	/** The identity runs are grouped by. */
	config_hash: string;
	/** Scored runs. */
	trials: number;
	passes: number;
	pass_rate: number;
}

/**
 * Agent variance per (task, agent config): how often the same config both passed and failed.
 * unstable counts configs with a pass rate strictly between 0 and 1. Normal for agents, not a defect.
 */
export interface OutcomeVariance {
	configs: number;
	unstable: number;
	/** Mean Bernoulli variance p(1-p) over configs with at least two scored trials. */
	mean_variance: number;
	per_config: ConfigOutcome[];
}

/** Runs that ended for reasons outside the agent. attempts includes infra retries and synthetic runs. */
export interface InfraHealth {
	attempts: number;
	aborted_infra: number;
	infra_error: number;
	invalid_evaluator: number;
	/** The agent hit its wall-time cap. */
	timeouts: number;
	/** (aborted_infra + infra_error + invalid_evaluator) / attempts. */
	rate: number;
	timeout_rate: number;
}

/** Observed difficulty label. unknown below the run or config minimum. */
export type ObservedDifficulty = "easy" | "medium" | "hard" | "unknown";

/** Declared against observed difficulty. */
export type DifficultyMatch = "match" | "mismatch" | "undeclared" | "unknown";

/** Whether a task separates agents at all. always_pass and always_fail carry no information. */
export type Discrimination = "discriminating" | "always_pass" | "always_fail" | "single_config" | "unknown";

/** Empirical difficulty from non-synthetic runs; interval is the Wilson score interval of pass_rate. */
export interface DifficultyHealth {
	/** task.yaml difficulty; "" when unset. */
	declared: string;
	observed: ObservedDifficulty;
	match: DifficultyMatch;
	pass_rate: number | null;
	interval: [number, number] | null;
	runs: number;
	configs: number;
	discrimination: Discrimination;
	/** Best minus worst config pass rate. */
	spread: number | null;
}

/** One task's health. */
export interface TaskHealth {
	task: string;
	split: TaskSplit;
	quarantine: TaskQuarantine | null;
	verifier: VerifierHealth;
	variance: OutcomeVariance;
	infra: InfraHealth;
	difficulty: DifficultyHealth;
	flags: HealthFlag[];
}

/** GET /suites/{id}/health and `suite health -json`. A reading of the record, never part of a lock. */
export interface SuiteHealth {
	suite: string;
	version: string;
	computed_at: string;
	/** Experiments of this suite id that were read. */
	experiments: number;
	/** Sealed attempts read, synthetic included. */
	runs: number;
	thresholds: HealthThresholds;
	tasks: TaskHealth[];
	/** Flag to the number of tasks carrying it. */
	counts: Record<string, number>;
	notes: string[];
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
	/** W9 routing policy under test; absent for the fixed single-model key. */
	routing?: RoutingPolicy;
	/** X8 voice agents: more models of the same provider the run's key may call (speech to text, text to speech). */
	models?: string[];
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
	runs_total?: number | null;
	last_run_at?: string | null;
	/** Effective capabilities (shell, tools, mcp, sessions, ...): the config's own list or its adapter defaults. */
	capabilities?: string[];
	error?: string;
	/** How the agent reports its own steps when its config shows it ("native" for the generic_jsonl parser); absent when not known from the config. */
	reports_events?: "native";
	/** The agent brings its own model (HTTP endpoint, A2A agent, OpenAI-compatible endpoint naming its model): evaluations may leave the model blank. */
	owns_model?: boolean;
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
export type Track =
	| "coding"
	| "terminal"
	| "tool_use"
	| "mcp"
	| "memory"
	| "skills"
	| "safety"
	| "qa"
	| "browser"
	| "computer_use"
	| "artifact"
	| "support"; // W3 customer support

/** The tracks in display order. */
export const TRACKS: readonly Track[] = [
	"coding",
	"terminal",
	"tool_use",
	"mcp",
	"memory",
	"skills",
	"safety",
	"qa",
	"browser",
	"computer_use",
	"artifact",
	"support", // W3 customer support
];

/** verified: a deterministic check. judged: a pinned judge model decides; never compared with verified. */
export type Grading = "verified" | "judged";

/** What the verifier reads (OutputSpec.Kind). */
export type OutputKind = "diff" | "answer" | "state" | "artifact";

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
	/** The files a task with output kind artifact must produce. */
	artifacts?: ArtifactSpec[];
}

/** One file, or a glob of files, an artifact task expects in the workspace (schemas.ArtifactSpec). */
export interface ArtifactSpec {
	/** Names the folder artifacts/<id> in the run folder. */
	id: string;
	/** Relative to the workspace root; may use *, ? and [...]. */
	path: string;
	/** A required artifact without one usable file fails the run's grade. */
	required?: boolean;
	/** Per file; absent means the default cap (32 MiB). */
	max_bytes?: number;
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
	/** Row kind (schemas/toolrows.go): absent for a tool call; otherwise an MCP handshake, listing, notification, server request, response, transport request or opaque traffic, never counted as a tool call. */
	kind?: ToolRowKind;
	http_method?: string;
	path?: string;
	/** Content type of the request. */
	content_type?: string;
	response_content_type?: string;
	/** Bytes relayed, counted over the whole body even when it was not kept. */
	request_bytes?: number;
	response_bytes?: number;
	/** The proxy relayed this traffic without parsing it; the reason says why. */
	unparsed?: boolean;
	unparsed_reason?: string;
}

/** Kinds of tool proxy ledger rows that are not tool calls (schemas/toolrows.go). */
export type ToolRowKind = "mcp_method" | "mcp_notification" | "mcp_server_request" | "mcp_response" | "transport" | "opaque";

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

/**
 * One artifact file of a run (schemas.ArtifactFile). Agent-written content: it is offered for
 * download from GET /runs/{id}/artifacts/{artifact}/{name} and never rendered.
 */
export interface ArtifactFile {
	/** The ArtifactSpec it matched. */
	id: string;
	/** Base name of the file; absent when the spec matched nothing. */
	name?: string;
	/** Relative to the run folder: artifacts/<id>/<name>; absent when not copied. */
	path?: string;
	/** Where it was read, relative to the workspace root (the spec's path when nothing matched). */
	source: string;
	sha256?: string;
	bytes: number;
	/** Why it was not copied: missing, outside the workspace, not a regular file, over a size cap. */
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
	/** Rejected uploads since it last registered; at 3 it is leased nothing more until it registers again. */
	rejected?: number;
	last_seen: string;
	/** X7: the GPUs the worker registered; only a worker with free GPUs is leased a GPU slot. */
	gpus?: GPUInfo[];
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
export type RegradeResponse = WithMode<Job>;
export type TriageResponse = WithMode<Triage>;
export type ReleaseListResponse = WithMode<ReleaseList>;
export type ReleaseResponse = WithMode<ReleaseRecord>;
export type LeaderboardResponse = WithMode<Leaderboard>;
export type TrendsResponse = WithMode<Trends>;
export type SuitesResponse = WithMode<SuiteList>;
export type SuiteTaskResponse = WithMode<SuiteTaskDetail>;
export type SuiteHealthResponse = WithMode<SuiteHealth>;
export type AgentsResponse = WithMode<AgentList>;
export type ImportsResponse = WithMode<ImportList>;
export type WorkersResponse = WithMode<WorkerList>;
export type ReviewQueueResponse = WithMode<ReviewQueue>;
export type ReviewLogResponse = WithMode<ReviewLog>;
export type RunReviewsResponse = WithMode<RunReviews>;
export type ReviewResponse = WithMode<Review>;

// ---------------------------------------------------------------------------------------------
// Forecast (POST /estimate, schemas/forecast.go, docs/forecast.md) and model matrix
// (GET /experiments/{id}/matrix, schemas/matrix.go, docs/model-matrix.md).
// ---------------------------------------------------------------------------------------------

/** What a forecast range holds: a range from history, an upper bound only, or nothing. */
export type ForecastRangeKind = "history" | "upper_bound" | "unknown";

/**
 * A low, median and high estimate (schemas.ForecastRange). history: all three set. upper_bound:
 * only high. unknown: none, and reason says why. A planning range, not a confidence interval.
 */
export interface ForecastRange {
	kind: ForecastRangeKind;
	low: number | null;
	median: number | null;
	high: number | null;
	reason?: string;
}

/** The 10th, 50th and 90th percentiles of one run over the arm's history, pooled across tasks. */
export interface PerRunRanges {
	cost_usd: ForecastRange;
	tokens: ForecastRange;
	wall_s: ForecastRange;
}

/** Where an arm's forecast comes from, in order of preference. */
export type ForecastBasis = "config_hash" | "agent_model" | "budget_bound" | "none";

/** One arm's forecast (schemas.ArmForecast). */
export interface ArmForecast {
	arm: string;
	label: string;
	agent: string;
	model: string;
	config_hash: string;
	/** Slots of this arm. */
	runs: number;
	basis: ForecastBasis;
	/** Plain words, for example "14 sealed runs of config 3f2a91c0 in 2 experiments". */
	basis_text: string;
	/** Sealed runs the estimate rests on. */
	history_runs: number;
	/** Experiments those runs came from. */
	experiments: string[];
	/** Planned tasks estimated from their own history; the rest come from the suite-wide history. */
	task_level: number;
	per_run: PerRunRanges;
	cost_usd: ForecastRange;
	tokens: ForecastRange;
	agent_time_s: ForecastRange;
}

/** The judge model's expected cost on a judged suite. Never part of the agent cost. */
export interface JudgeForecast {
	/** Runs that will be judged. */
	runs: number;
	/** Past judge records it rests on. */
	history_runs: number;
	cost_usd: ForecastRange;
	basis_text: string;
}

/** Forecast fit against budget.total_usd. */
export type ForecastFit = "fits" | "may_exceed" | "likely_exceeds" | "exceeds" | "unknown" | "no_budget";

/**
 * Expected cost and time of a planned experiment, from the sealed history of the same agent
 * configs on the same suite (schemas.Forecast). A planner refusal answers fit "unknown" with the
 * reason in notes.
 */
export interface Forecast {
	exp_id: string;
	/** id@version */
	suite: string;
	generated_at: string;
	/** Price snapshot the per-run bounds were checked against. */
	price_ref?: string;
	arms: ArmForecast[];
	/** Agent cost of every slot; judge cost is separate. */
	cost_usd: ForecastRange;
	/** Sum of run wall times. */
	agent_time_s: ForecastRange;
	/** Agent time over concurrency, no queueing assumed. */
	wall_clock_s: ForecastRange;
	concurrency: number;
	/** Judged suites only. */
	judge?: JudgeForecast;
	/** budget.total_usd; 0 when none is set. */
	budget_usd: number;
	fit: ForecastFit;
	fit_reason: string;
	/** One sentence: where the numbers come from. */
	basis: string;
	notes: string[];
}

/** One arm of a model matrix: the arm id and its gateway provider beside the leaderboard entry numbers. */
export interface MatrixRow extends LeaderboardEntry {
	arm: string;
	provider: string;
}

/** The paired comparison of two arms. mean_diff is the mean over common tasks of arm_x's success rate minus arm_y's. */
export interface MatrixPair {
	arm_x: string;
	arm_y: string;
	/** Common tasks. */
	tasks: number;
	/** Common tasks where the rates differ. */
	n_nonzero: number;
	mean_diff: number;
	/** Raw two-sided sign-flip p; null without common tasks. */
	p: number | null;
	/** Monte Carlo sign vectors behind p; absent when p is exact. */
	p_draws?: number;
	/** Holm-adjusted across all pairs; null without common tasks. */
	p_adjusted: number | null;
	significant: boolean;
	/** Plain words, for example "cannot separate: too few differing tasks (2 of 6)". */
	reason: string;
}

/** Every arm of one experiment on one condition, with every pair tested (schemas.ModelMatrix). */
export interface ModelMatrix {
	exp_id: string;
	/** id@version */
	suite: string;
	condition: string;
	/** Conditions of the experiment with a graded run. */
	conditions: string[];
	grade_source: string;
	alpha: number;
	/** Tasks every arm has a graded run on. */
	tasks: number;
	/** False when no pair could reach alpha after Holm, whatever the results. */
	separable: boolean;
	/** Plain words: why nothing can be separated, or what the tiers mean. */
	note: string;
	/** Scripted agent, scripted model or smoke experiment: numbers are not findings. */
	synthetic: boolean;
	/** In rank order. */
	rows: MatrixRow[];
	/** Every pair of rows, in rank order of the first then the second. */
	pairs: MatrixPair[];
	excluded: LeaderboardExclusion[];
	/** held_out when the matrix reads only held-out tasks; absent: public tasks only. */
	split?: TaskSplit;
	/** The experiment's newest verdict is INVALID: no run is ranked, excluded says why. */
	invalid?: boolean;
}

/** GET /experiments/{id}/matrix query. An absent or empty param is left out so the server default applies. */
export interface ModelMatrixParams {
	expId: string;
	condition?: string;
	/** "original" or a regrade id. */
	grade_source?: string;
	alpha?: string;
}

export type ForecastResponse = WithMode<Forecast>;
export type ModelMatrixResponse = WithMode<ModelMatrix>;

// ---------------------------------------------------------------------------------------------
// clusters: failure clusters. Failed verified runs grouped by a deterministic signature built from
// their recorded evidence (docs/failure-clusters.md). A cluster is an observation, not a root cause.
// ---------------------------------------------------------------------------------------------

/** exclude: live runs only. include: live and synthetic. only: scripted agent or scripted model runs only. */
export type ClusterSynthetic = "exclude" | "include" | "only";

/** Every synthetic filter value, in switch order. */
export const CLUSTER_SYNTHETIC = ["exclude", "include", "only"] as const satisfies readonly ClusterSynthetic[];

/** How far a cluster reaches: one run, one agent on one task, one agent, one task, or many agents and tasks. */
export type ClusterScope = "single_run" | "agent_and_task" | "agent" | "task" | "systemic";

/** The layers a signature is built from, most specific first. */
export type ClusterLayerKind =
	| "outcome"
	| "failed_tests"
	| "failed_dimensions"
	| "answer_category"
	| "state_checks"
	| "gates"
	| "loop"
	| "error"
	| "provider"
	| "last_tool"
	| "condition";

/** The filters a cluster list or detail was computed with, as the server resolved them. */
export interface ClusterFilters {
	suite: string;
	agent: string;
	experiments: string[];
	since: string;
	min: number;
	synthetic: string;
}

/** Runs left out for one reason: "synthetic", "not sealed", "seal does not verify", "not counted" or "evidence problem". */
export interface ClusterExclusion {
	reason: string;
	runs: number;
}

/** One part of a signature. items holds ids (hidden test ids, gate names); source says where an error line came from. */
export interface ClusterLayer {
	kind: ClusterLayerKind;
	label: string;
	items?: string[];
	source?: string;
}

/** Another cluster that shares part of this one's signature. */
export interface ClusterRelated {
	signature: string;
	title: string;
	count: number;
	reason: string;
}

/** The agent's config hash changed and the cluster's runs all came after the change. */
export interface ClusterConfigChange {
	agent: string;
	from_hash: string;
	to_hash: string;
	/** RFC3339 */
	at: string;
}

/** One failure cluster (clusters.Cluster). */
export interface ClusterSummary {
	/** "c_" + 16 hex; safe in a URL. */
	signature: string;
	title: string;
	outcome: Outcome;
	/** Most specific first. */
	layers: ClusterLayer[];
	count: number;
	/** 0..1 of the failures in scope. */
	share: number;
	/** "suite/task" */
	tasks: string[];
	/** Agent config ids. */
	agents: string[];
	config_hashes: string[];
	conditions: string[];
	experiments: string[];
	/** RFC3339 */
	first_seen: string;
	/** RFC3339 */
	last_seen: string;
	/** Up to 3 run ids. */
	examples: string[];
	scope: ClusterScope;
	after_config_change?: ClusterConfigChange;
	related?: ClusterRelated[];
	/** The normalized error fingerprint. Agent-controlled text: render inert, never as markup or a link. */
	error_text?: string;
}

/** GET /clusters: failures among verified, counted, sealed runs, grouped by signature. */
export interface ClusterList {
	/** RFC3339 */
	generated_at: string;
	filters: ClusterFilters;
	/** Verified, counted, sealed runs in scope, passing ones included. */
	runs: number;
	/** Of runs, those that did not pass. */
	failures: number;
	/** Experiments read. */
	experiments: string[];
	/** Sorted by count, largest first. */
	clusters: ClusterSummary[];
	/** Clusters below min, not listed. */
	hidden: number;
	/** Runs left out, by reason. */
	excluded: ClusterExclusion[];
}

/** One run of a cluster. */
export interface ClusterRun {
	run_id: string;
	/** The run's matched pair, for the run page link (/runs?pair=). Absent when the run has none. */
	pair_id?: string;
	exp_id: string;
	suite: string;
	task: string;
	arm: string;
	condition: string;
	agent: string;
	config_hash: string;
	outcome: Outcome;
	/** RFC3339 */
	at: string;
	last_tool?: string;
	last_tool_errored?: boolean;
	/** The run's normalized error line. Agent-controlled text: render inert. */
	error_text?: string;
}

/** GET /clusters/{signature}: one cluster and its runs. 404 when the signature is not in the filtered set. */
export interface ClusterDetail {
	/** RFC3339 */
	generated_at: string;
	filters: ClusterFilters;
	failures: number;
	cluster: ClusterSummary;
	runs: ClusterRun[];
}

/** Query of GET /clusters and GET /clusters/{signature}. An absent or empty field takes the server default. */
export interface ClusterParams {
	suite?: string;
	agent?: string;
	/** Experiment ids, comma separated. */
	exp?: string;
	since?: string;
	min?: number;
	synthetic?: ClusterSynthetic;
}

export type ClusterListResponse = WithMode<ClusterList>;
export type ClusterDetailResponse = WithMode<ClusterDetail>;

// ---------------------------------------------------------------------------------------------
// Agent diff (agentdiff/agentdiff.go): what changed between version A and version B of an
// agent, and which tasks flipped. GET /agents/diff and GET /agents/diff/versions.
// ---------------------------------------------------------------------------------------------

/** Config groups, in the order the report lists them. */
export const AGENT_DIFF_GROUPS = ["model", "prompt_notes", "tools", "budgets", "gateway", "image", "other"] as const;
export type AgentDiffGroupId = (typeof AGENT_DIFF_GROUPS)[number];

/** Task classes: stable flips, unstable moves, and the unchanged rest. */
export const AGENT_DIFF_CLASSES = [
	"flipped_to_pass",
	"flipped_to_fail",
	"moved_to_pass",
	"moved_to_fail",
	"unstable_in_both",
	"unchanged_pass",
	"unchanged_fail",
] as const;
export type AgentDiffClass = (typeof AGENT_DIFF_CLASSES)[number];

/** Conclusion of the exact paired sign-flip test on per-task pass rate differences B minus A. */
export type AgentDiffConclusion = "b_better" | "b_worse" | "no_clear_difference" | "cannot_tell";
/** A metric delta read with the metric's direction. */
export type AgentDiffDirection = "better" | "worse" | "same" | "unknown";
/** Change kind of a config field or a laid-down file. */
export type AgentDiffKind = "changed" | "added" | "removed";

/** Runs of a version left out for one reason. */
export interface AgentDiffExclusion {
	reason: string;
	runs: number;
}

/** One side of the diff. */
export interface AgentDiffVersion {
	/** As asked: a config hash, a prefix, or <exp_id>:<arm>. */
	selector: string;
	config_hash: string;
	agent_id: string;
	label: string;
	/** Set when picked by <exp_id>:<arm>; only that arm's runs count. */
	exp_id?: string;
	arm?: string;
	/** Experiments whose verified runs entered. */
	experiments: string[];
	/** Verified runs on the suite and condition. */
	runs: number;
	excluded: AgentDiffExclusion[];
}

/** One dotted config path that differs. old is null when added, new is null when removed. */
export interface AgentDiffFieldChange {
	path: string;
	kind: AgentDiffKind;
	old: JsonValue;
	new: JsonValue;
	/** Both sides read "[redacted]". */
	redacted?: boolean;
}

/** One laid-down file whose hash differs. diff is unified text; diff_note says why there is none. */
export interface AgentDiffFileChange {
	path: string;
	kind: AgentDiffKind;
	old_hash?: string;
	new_hash?: string;
	/** Unified diff text. Inert: render as text only. */
	diff?: string;
	diff_note?: string;
	/** The diff was cut at the line cap. */
	truncated?: boolean;
}

/** The changes of one config group. */
export interface AgentDiffConfigGroup {
	id: AgentDiffGroupId;
	fields: AgentDiffFieldChange[];
	files: AgentDiffFileChange[];
}

/** The config half of the report. */
export interface AgentDiffConfig {
	identical: boolean;
	/** Field and file changes over every group. */
	changes: number;
	/** Non-empty groups in AGENT_DIFF_GROUPS order. */
	groups: AgentDiffConfigGroup[];
}

/** Common tasks per class. */
export type AgentDiffClassCounts = Record<AgentDiffClass, number>;

/** The exact paired sign-flip test. p is null when there is no common task. */
export interface AgentDiffSignTest {
	n_tasks: number;
	n_nonzero: number;
	n_positive: number;
	n_negative: number;
	p: number | null;
	exact: boolean;
	/** Smallest p the changed tasks allow. */
	min_possible_p: number;
	alpha: number;
	conclusion: AgentDiffConclusion;
}

/** One sealed run. pair_id opens it in the run comparison view. */
export interface AgentDiffRunRef {
	run_id: string;
	exp_id: string;
	pair_id: string;
	outcome: string;
}

/** One run of each version on a task. pair_id is set when both runs are one experiment pair. */
export interface AgentDiffRunPair {
	a: AgentDiffRunRef;
	b: AgentDiffRunRef;
	pair_id?: string;
}

/** One common task. */
export interface AgentDiffTask {
	task: string;
	class: AgentDiffClass;
	pass_a: number;
	n_a: number;
	pass_b: number;
	n_b: number;
	rate_a: number;
	rate_b: number;
	/** Representative runs; set for flipped and moved tasks. */
	pair?: AgentDiffRunPair;
}

/** One metric over the common tasks, paired per task. */
export interface AgentDiffMetric {
	metric: string;
	higher_is_better: boolean;
	a: number | null;
	b: number | null;
	/** B minus A. */
	delta: number | null;
	/** Tasks with an observed value on both sides. */
	tasks: number;
	unknown_a: number;
	unknown_b: number;
	direction: AgentDiffDirection;
}

/** One gate that fired in either version. new: fired in B and never in A; tasks: where it did. */
export interface AgentDiffGate {
	gate: string;
	fired_a: number;
	fired_b: number;
	new: boolean;
	tasks: string[];
}

/** The outcome half of the report. */
export interface AgentDiffOutcome {
	common_tasks: number;
	/** Tasks only A ran (verified) on the suite and condition. */
	only_a: string[];
	only_b: string[];
	/** Mean per-task pass rate over the common tasks. */
	pass_rate_a: number;
	pass_rate_b: number;
	counts: AgentDiffClassCounts;
	/** flipped_to_pass minus flipped_to_fail. */
	net: number;
	test: AgentDiffSignTest;
	/** Flips first, then moves, unstable, unchanged; by task id within a class. */
	tasks: AgentDiffTask[];
	metrics: AgentDiffMetric[];
	gates: AgentDiffGate[];
}

/** GET /agents/diff */
export interface AgentDiffReport {
	a: AgentDiffVersion;
	b: AgentDiffVersion;
	/** id@version */
	suite: string;
	condition: string;
	synthetic: boolean;
	config: AgentDiffConfig;
	outcome: AgentDiffOutcome;
	/** One plain sentence or three. */
	summary: string;
}

/** One experiment arm that resolved to a config hash. */
export interface AgentDiffArmRef {
	exp_id: string;
	arm: string;
	label: string;
	suite: string;
	created_at: string;
}

/** One config hash with the arms that ran it. */
export interface AgentDiffVersionInfo {
	config_hash: string;
	agent_id: string;
	/** Newest arm's label. */
	label: string;
	suites: string[];
	/** Newest first. */
	arms: AgentDiffArmRef[];
	/** Sealed runs, not yet seal-verified. */
	sealed_runs: number;
	latest_at: string;
	version?: number;
	model?: string;
	system?: boolean;
}

/** GET /agents/diff/versions: every config hash the run folder knows. */
export interface AgentDiffVersionList {
	versions: AgentDiffVersionInfo[];
}

/** GET /agents/diff query. An absent or empty param is left out so the server default applies. */
export interface AgentDiffParams {
	a?: string;
	b?: string;
	suite?: string;
	condition?: string;
	/** With a and b empty: the two newest config hashes of this agent id. */
	agent?: string;
	/** "1" includes synthetic runs. */
	synthetic?: string;
}

export type AgentDiffResponse = WithMode<AgentDiffReport>;
export type AgentDiffVersionsResponse = WithMode<AgentDiffVersionList>;

// =============================================================================================
// Hosted platform (docs/platform-design.md): auth, organizations, members, API tokens, jobs,
// runners and the audit log. Platform routes answer with `mode` like every bench route. Times
// are RFC 3339 strings; formatWhen() also reads unix milliseconds.
// =============================================================================================

/** auth.mode: local is the single-user mode (write token); hosted has sessions and orgs. */
export type AuthMode = "local" | "hosted";

/** A membership role, least to most privileged. */
export type Role = "viewer" | "member" | "admin" | "owner";

/** How a request authenticated (auth.PrincipalKind). */
export type PrincipalKind = "user" | "token" | "runner" | "anonymous" | "local";

/** auth.signup: who may create an account. */
/** Who may sign up from the web (auth/signupmode.go). allowlist: an invitation, or an address at an allowed domain. */
export type SignupPolicy = "invite" | "allowlist" | "open" | "closed";

/** A time from a platform route: RFC 3339, or unix milliseconds. */
export type PlatformTime = string | number;

/** A signed-in user (auth.User). */
export interface PlatformUser {
	id: string;
	email: string;
	name: string;
	created_at: PlatformTime;
}

/** One organization of the caller with the caller's role in it (auth.MeOrg). */
export interface MeOrg {
	id: string;
	slug: string;
	name: string;
	role: Role;
}

/** The session's organization as the UI holds it: an org of /auth/me, public_read once known. */
export interface OrgSummary {
	id: string;
	slug: string;
	name: string;
	role?: Role;
	public_read?: boolean;
}

/** GET /auth/me (auth.Me): who is calling, the organizations, the current one and the CSRF value. */
export interface AuthMe {
	auth_mode: AuthMode;
	kind: PrincipalKind;
	user?: PlatformUser;
	token_id?: string;
	orgs: MeOrg[];
	/** Id of the organization requests act in. */
	current_org?: string;
	default_org?: string;
	org_reset?: boolean;
	/** The caller's role in current_org. */
	role?: Role;
	/** Session CSRF value: sent as X-Heimdall-CSRF on every cookie-authenticated write. */
	csrf?: string;
	/** A server admin: listed by user id, granted from the CLI, or a verified listed email (auth/serveradmin.go). */
	server_admin?: boolean;
	/** Set for a signed-in user: whether their email is verified (auth/verify.go). Absent on older servers. */
	email_verified?: boolean;
}

/** GET /auth/config (auth.AuthInfo): what the sign-in page offers, answered to anyone. */
export interface AuthConfig {
	auth_mode: AuthMode;
	signup?: SignupPolicy;
	github: boolean;
	/** An empty install: the first sign-up creates the owner account and its organization. */
	bootstrap?: boolean;
	/** Sign-up needs an hCaptcha token (HEIMDALL_CAPTCHA_SECRET is set). */
	captcha?: boolean;
	/** hCaptcha site key for the sign-up widget. */
	captcha_site_key?: string;
	/** Under signup allowlist: the email domains that may sign up without an invitation. */
	signup_domains?: string[];
	/** email: verification links are mailed; log: they go to the server log and the server admin passes them on. */
	email_delivery?: EmailDelivery;
}

/** Where verification links go (auth/verify.go). */
export type EmailDelivery = "email" | "log";

/** POST /auth/login. */
export interface LoginRequest {
	email: string;
	password: string;
}

/** POST /auth/signup. invitation is the token from an invitation link. */
export interface SignupRequest {
	email: string;
	password: string;
	name?: string;
	/** Name of the first organization (open sign-up); default from the name or email. */
	org_name?: string;
	invitation?: string;
}

/** GET /auth/invitations/{token} (auth.InvitationInfo): what a link grants, before it is accepted. */
export interface InvitationPreview {
	org_name: string;
	org_slug: string;
	email: string;
	role: Role;
	expires_at: PlatformTime;
	/** The invited email has an account: offer sign-in rather than sign-up. */
	account_exists: boolean;
	/** False under signup closed: only an existing account may accept. */
	signup_allowed: boolean;
}

/** POST /auth/password (auth.PasswordChangeRequest). current_password may be empty when the account has none yet. */
export interface PasswordChangeRequest {
	current_password: string;
	new_password: string;
}

/** POST /auth/switch-org: an org id or slug the user belongs to. */
export interface SwitchOrgRequest {
	org: string;
}

/** One membership (handlers/members.go MemberInfo). */
export interface Member {
	user_id: string;
	email: string;
	name?: string;
	role: Role;
	created_at: PlatformTime;
}

/** GET /orgs/{org}/members. */
export interface MemberList {
	members: Member[];
}

/** One invitation (handlers/members.go InvitationInfo). token is set only in the create answer. */
export interface Invitation {
	id: string;
	email: string;
	role: Role;
	created_by?: string;
	created_at: PlatformTime;
	expires_at: PlatformTime;
	accepted_at?: PlatformTime;
	token?: string;
	/** The acceptance link, with token: in the create answer only. */
	url?: string;
}

/** GET /orgs/{org}/invitations. */
export interface InvitationList {
	invitations: Invitation[];
}

/** POST /orgs/{org}/invitations. */
export interface InvitationRequest {
	email: string;
	role: Role;
}

/** One organization as the org routes answer (handlers/orgs.go OrgInfo). */
export interface OrgInfo {
	id: string;
	slug: string;
	name: string;
	public_read: boolean;
	created_at: PlatformTime;
	/** The caller's role in the org, empty for a public reader. */
	role?: Role;
}

/** GET /orgs. */
export interface OrgList {
	orgs: OrgInfo[];
}

/** An API token, never its secret (auth.APIToken). */
export interface ApiToken {
	id: string;
	org_id: string;
	/** The user who created it; empty once that user is removed. */
	user_id?: string;
	name: string;
	/** hm_<prefix>: enough to recognise the token, not to use it. */
	prefix: string;
	role: Role;
	/** What the token may do below its role (auth.Scopes); absent on tokens made before scopes, which the role alone limits. */
	scopes?: ApiTokenScope[];
	created_at: PlatformTime;
	last_used_at?: PlatformTime;
	expires_at?: PlatformTime;
	revoked_at?: PlatformTime;
}

/** API token scopes, narrowest first: read, evaluate (read plus running evaluations), admin (everything the role allows). */
export type ApiTokenScope = "read" | "evaluate" | "admin";

/** GET /auth/tokens. */
export interface ApiTokenList {
	tokens: ApiToken[];
}

/** POST /auth/tokens (auth.TokenCreateRequest). role empty: the creator's own; 0 days: never expires. */
export interface ApiTokenRequest {
	name: string;
	role?: Role;
	expires_in_days?: number;
	scopes?: ApiTokenScope[];
}

/** POST /auth/tokens (auth.TokenCreated): the secret, shown once, and the token row. */
export interface ApiTokenCreated {
	token: string;
	api_token: ApiToken;
}

/** Runner state (schemas/runners.go): lost after the stale threshold without a call. */
export type RunnerState = "idle" | "running" | "lost";

/** One registered runner (schemas.RunnerView). */
export interface Runner {
	id: string;
	org_id: string;
	/** The runner's -name, its worker id. */
	name: string;
	pool: string;
	labels: string[] | null;
	/** Bench build the runner runs. */
	version: string;
	host: string;
	state: RunnerState;
	slots: number;
	/** Slot ids it holds now. */
	running: string[] | null;
	running_slots: number;
	/** Runs accepted since the server started. */
	completed: number;
	token_id: string;
	token_prefix?: string;
	last_seen: PlatformTime;
	registered_at: PlatformTime;
	/** The coordinator holds a registration for it now. */
	live: boolean;
}

/** GET /runners. */
export interface RunnerList {
	runners: Runner[];
}

/** Runner token state. */
export type RunnerTokenState = "active" | "revoked" | "expired";

/** A runner token without its secret (schemas.RunnerTokenView). */
export interface RunnerToken {
	id: string;
	org_id: string;
	pool: string;
	name: string;
	/** The 8 lookup characters after hmr_. */
	prefix: string;
	created_by: string;
	created_at: PlatformTime;
	expires_at?: PlatformTime;
	last_used_at?: PlatformTime;
	revoked_at?: PlatformTime;
	state: RunnerTokenState;
}

/** GET /runners/tokens. */
export interface RunnerTokenList {
	tokens: RunnerToken[];
}

/** POST /runners/tokens. Every field optional: pool defaults to the server's default pool. */
export interface RunnerTokenRequest {
	pool?: string;
	name?: string;
	expires_in_days?: number;
}

/** POST /runners/tokens: the only time the token is shown, with the lines that use it. */
export interface RunnerTokenCreated {
	token: string;
	info: RunnerToken;
	/** The exact `bifrost-bench runner start` line. */
	command: string;
	/** The install script line for a fresh box. */
	install: string;
}

/** One audit_log row (handlers/orgs.go AuditRow). */
export interface AuditEntry {
	id: string;
	actor_type: "user" | "token" | "runner" | "system" | "anonymous";
	actor_id?: string;
	action: string;
	target_type?: string;
	target_id?: string;
	ip?: string;
	at: PlatformTime;
	details?: Record<string, JsonValue>;
}

/** GET /orgs/{org}/audit, newest first. */
export interface AuditList {
	entries: AuditEntry[];
	/** Opaque cursor of the next page (pass back as cursor); absent on the last page. */
	next?: string;
}

/** GET /orgs/{org}/audit query: cursor is AuditList.next of the previous page; before is unix milliseconds (rows strictly older). */
export interface AuditLogParams {
	orgId: string;
	action?: string;
	cursor?: string;
	before?: number;
	limit?: number;
}

/** POST /orgs/{org}/rename. */
export interface RenameOrgRequest {
	name: string;
}

/** POST /orgs/{org}/public-read. */
export interface PublicReadRequest {
	public_read: boolean;
}

/** jobs.status */
export type JobStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled";

/** One job (jobs.Job): experiment runs, suite checks, regrades, imports. */
export interface Job {
	id: string;
	org_id: string;
	kind: string;
	/** What the job acts on, for example an experiment id. */
	target?: string;
	status: JobStatus;
	params: Record<string, JsonValue> | null;
	/** Kind-specific: experiment.run reports {exp_id, phase, slots, sealed, skipped, errors, last_event}. */
	progress?: Record<string, JsonValue>;
	result?: Record<string, JsonValue>;
	error?: string;
	created_by?: string;
	/** How created_by is shown (GET /jobs/{id}); an email in created_by is sent to org admins only. */
	created_by_name?: string;
	created_at: PlatformTime;
	started_at?: PlatformTime;
	finished_at?: PlatformTime;
	cancel_requested: boolean;
	/** Claims so far: 1 on the first run, more after each requeue (a resume). */
	attempt: number;
	lease_owner?: string;
	lease_until?: PlatformTime;
	/** Claim order among equally loaded organizations, -10 to 10; absent is 0 (POST /jobs/{id}/priority). */
	priority?: number;
}

/** GET /jobs (handlers/jobs.go JobList): newest first, and the kinds POST /jobs accepts. */
export interface JobList {
	jobs: Job[];
	/** The before= cursor of the next page, empty on the last. */
	next?: string;
	kinds: string[];
}

/** One line of a job's log (jobs.LogLine). */
export interface JobLogLine {
	seq: number;
	at: PlatformTime;
	level: string;
	line: string;
}

/** GET /jobs/{id}/logs?since=N (jobs.LogPage): lines after seq N, and the job's status. */
export interface JobLogs {
	job_id: string;
	status: JobStatus;
	lines: JobLogLine[];
	/** Cursor for the next poll. */
	next: number;
	/** Lines left out between the oldest and newest kept lines. */
	dropped: number;
}

/** POST /experiments/{id}/regrade (handlers/experimentjobs.go RegradeRequest); every field optional. */
export interface ExperimentRegradeRequest {
	/** Regrade id to write; default a new rg_ id. */
	id?: string;
	/** Why; every report that uses the regrade shows it. */
	note?: string;
	/** The suite.lock the experiment was frozen with, when the suite's lock changed since. */
	suite_lock?: string;
}

export type AuthMeResponse = WithMode<AuthMe>;
export type AuthConfigResponse = WithMode<AuthConfig>;
export type MeOrgResponse = WithMode<MeOrg>;
export type InvitationPreviewResponse = WithMode<InvitationPreview>;
export type MemberResponse = WithMode<Member>;
export type MemberListResponse = WithMode<MemberList>;
export type InvitationListResponse = WithMode<InvitationList>;
export type InvitationResponse = WithMode<Invitation>;
export type OrgResponse = WithMode<OrgInfo>;
export type OrgListResponse = WithMode<OrgList>;
export type ApiTokenListResponse = WithMode<ApiTokenList>;
export type ApiTokenCreatedResponse = WithMode<ApiTokenCreated>;
export type RunnerListResponse = WithMode<RunnerList>;
export type RunnerTokenResponse = WithMode<RunnerToken>;
export type RunnerTokenListResponse = WithMode<RunnerTokenList>;
export type RunnerTokenCreatedResponse = WithMode<RunnerTokenCreated>;
export type AuditListResponse = WithMode<AuditList>;
export type JobListResponse = WithMode<JobList>;
export type JobResponse = WithMode<Job>;
export type JobLogsResponse = WithMode<JobLogs>;

// ---------------------------------------------------------------------------------------------
// Failure inbox area (handlers/inbox.go, traceinbox, docs/failure-inbox.md): production failures
// pulled from trace sources, grouped by signature, and turned into draft regression tasks.
// GET /inbox, GET /inbox/{id}, POST /inbox/{id}/accept|reject|snooze, POST /inbox/ingest.
// Key lists: handlers/testdata/contract/Inbox*.keys. Failure inbox area ends at the end of this block.
// ---------------------------------------------------------------------------------------------

/** Where an inbox item stands. */
export type InboxState = "new" | "accepted" | "rejected" | "duplicate" | "snoozed";
export const INBOX_STATES = ["new", "accepted", "rejected", "duplicate", "snoozed"] as const satisfies readonly InboxState[];

/** The task template an accepted item is drafted from. */
export type InboxTaskKind = "qa" | "tool-use" | "mcp" | "memory";
export const INBOX_TASK_KINDS = ["qa", "tool-use", "mcp", "memory"] as const satisfies readonly InboxTaskKind[];

/** How the traced request ended. */
export type InboxOutcome = "error" | "ok" | "unknown";

/** Whether the regression task drafted from an item passes in its newest experiment. */
export type InboxReleaseState = "passes" | "fails" | "not_run";

/** The parts the item signature is hashed from. Text is normalised: numbers read <n>. */
export interface InboxSignatureParts {
	request: string;
	tool?: string;
	error?: string;
}

/** One trace that matched the item's signature. */
export interface InboxTraceRef {
	id: string;
	source: string;
	at: string;
	/** The trace in its source's own UI. */
	link?: string;
}

/** The draft task an accepted item became. */
export interface InboxTaskLink {
	suite: string;
	id: string;
	/** Task folder, relative to the bench root. */
	dir: string;
	accepted_at: string;
	/** True once the drafted task passed admission. */
	admitted: boolean;
}

/** The drafted task's result in the newest experiment that ran it. */
export interface InboxReleaseStatus {
	status: InboxReleaseState;
	experiment?: string;
	at?: string;
	passed: number;
	runs: number;
}

/** One failure signature seen in production, with every trace that matched it. */
export interface InboxItem {
	id: string;
	state: InboxState;
	/** Source name from inbox/sources.yaml. */
	source: string;
	source_kind: string;
	trace_id: string;
	/** The first trace in its source's own UI. */
	link?: string;
	/** The request, redacted and cut short. */
	title: string;
	/** Why the trace was flagged. */
	flagged: string[];
	signature: string;
	signature_parts: InboxSignatureParts;
	occurrences: number;
	first_seen: string;
	last_seen: string;
	/** Newest last, at most 20. */
	traces: InboxTraceRef[];
	suggested_kind: InboxTaskKind;
	suggested_suite?: string;
	tools?: string[];
	model?: string;
	outcome: InboxOutcome;
	/** A passing session sampled as a regression golden; its accept starts from "the recorded session was right". */
	golden?: boolean;
	/** Values removed before storage, counted by kind. Never the values. */
	redacted?: Record<string, number>;
	/** Set once accepted. */
	task?: InboxTaskLink;
	/** Occurrences seen after the item was accepted. */
	recurrences?: number;
	last_recurrence?: string;
	release?: InboxReleaseStatus;
	/** Reject reason. */
	reason?: string;
	duplicate_of?: string;
	snoozed_until?: string;
	updated: string;
}

/** One trace source and its last pull. */
export interface InboxSourceStatus {
	name: string;
	kind: string;
	last_pull?: string;
	/** Traces newer than this are pulled next. */
	since?: string;
	last_error?: string;
	/** Traces read by the last pull. */
	pulled?: number;
}

/** A suite an item can be accepted into, with the template its tasks use. */
export interface InboxSuite {
	id: string;
	template: string;
}

/** GET /inbox: items in the filter, with counts by state over every item. */
export interface InboxList {
	items: InboxItem[];
	counts: Partial<Record<InboxState, number>>;
	sources: InboxSourceStatus[];
	suites: InboxSuite[];
	/** False when POST /inbox/ingest is off on this server. */
	ingest_enabled: boolean;
	/** Tasks drafted from accepted items. */
	regression_tasks: number;
}

/** GET /inbox/{id}: one item and its redacted preview (markdown text, at most 16 KiB). */
export interface InboxItemDetail {
	item: InboxItem;
	/** Plain text to show as is. Never rendered as HTML. */
	preview: string;
	/** The redacted requests, tool calls and final answer, for the "what should have happened" form. */
	session?: InboxSession;
}

/** One recorded tool call: arguments as JSON text, result cut short. */
export interface InboxSessionCall {
	seq: number;
	tool: string;
	args: string;
	result?: string;
	is_error: boolean;
}

/** The redacted session of an item, for the accept form. */
export interface InboxSession {
	requests: string[];
	calls: InboxSessionCall[];
	final_answer?: string;
}

/** The draft task an accept wrote. It still needs a person to finish, validate and admit it. */
export interface InboxDraft {
	/** suite/task. */
	task: string;
	dir: string;
	template: string;
	/** Files with EDIT ME markers, relative to dir. */
	edit_me: string[];
	/** A guided accept wrote every file; only the readiness check is left. */
	finished: boolean;
	/** Next steps, in order. */
	next: string[];
	warnings?: string[];
}

/** POST /inbox/{id}/accept. */
export interface InboxAcceptResult {
	item: InboxItem;
	draft: InboxDraft;
}

/** POST /inbox/{id}/reject and POST /inbox/{id}/snooze. */
export interface InboxActionResult {
	item: InboxItem;
}

/** POST /inbox/ingest. */
export interface InboxIngestResult {
	item_id: string;
	created: boolean;
	occurrences: number;
	/** Passing sessions not kept: the source's daily golden sample was full. */
	skipped?: number;
}

/** GET /inbox query. An absent or empty param is left out. */
export interface InboxListParams {
	state?: InboxState;
	source?: string;
}

/** POST /inbox/{id}/accept body. kind defaults to the suggested kind, task_id to a generated id. */
export interface InboxAcceptRequest {
	suite: string;
	kind?: InboxTaskKind;
	task_id?: string;
	/** "What should have happened": with it the task is written finished, not as an EDIT ME draft. */
	guide?: InboxGuide;
	/** Move the benchmark to grader checks (a guided tool-use or mcp task needs it). */
	switch_grader?: boolean;
	/** Pins the judge a reply rubric needs. */
	judge?: InboxJudge;
}

/** One tool call of a guide; args must be a subset of the call's arguments. */
export interface InboxGuideCall {
	tool: string;
	args?: Record<string, unknown>;
}

/** What should have happened in a recorded session. */
export interface InboxGuide {
	/** Replaces the first user request; empty keeps it as sent. */
	instruction?: string;
	/** Adds the user's later messages to the instruction, in order. */
	keep_turns?: boolean;
	/** The recorded answer or tool calls were right (a golden). */
	use_recorded?: boolean;
	/** qa and memory: the right answer, one fact per entry. */
	answers?: string[];
	match?: "exact" | "contains_all" | "regex" | "numeric" | "choice";
	wrong_answers?: string[];
	recorded_wrong?: boolean;
	/** tool-use and mcp: calls a correct agent makes, in order when ordered. */
	calls?: InboxGuideCall[];
	/** Calls it must never make. */
	forbidden?: InboxGuideCall[];
	ordered?: boolean;
	/** Judged rubric for the final reply. */
	reply_rubric?: string;
}

/** The judge of a reply rubric. */
export interface InboxJudge {
	provider: string;
	model: string;
}

/** POST /inbox/accept body: several items into one benchmark with one shared guide. */
export interface InboxBulkAcceptRequest {
	ids: string[];
	suite: string;
	kind?: InboxTaskKind;
	guide?: InboxGuide;
	switch_grader?: boolean;
	judge?: InboxJudge;
}

/** POST /inbox/accept answer: each item accepted, or why not. */
export interface InboxBulkAcceptResult {
	accepted: InboxAcceptResult[];
	failed: { id: string; error: string }[];
}

/** POST /inbox/{id}/reject body. With duplicate_of the item becomes a duplicate. */
export interface InboxRejectRequest {
	reason: string;
	duplicate_of?: string;
}

/** POST /inbox/{id}/snooze body: 1 to 90 days. */
export interface InboxSnoozeRequest {
	days: number;
}

/** POST /inbox/ingest body. Sent by services with the ingest token, never by this UI. */
export interface InboxIngestRequest {
	/** OTLP, Langfuse or chat JSON. */
	trace: unknown;
	/** Empty to detect. */
	format?: string;
	reason?: string;
	link?: string;
	source?: string;
}

export type InboxListResponse = WithMode<InboxList>;
export type InboxItemDetailResponse = WithMode<InboxItemDetail>;
export type InboxAcceptResponse = WithMode<InboxAcceptResult>;
export type InboxActionResponse = WithMode<InboxActionResult>;
export type InboxIngestResponse = WithMode<InboxIngestResult>;
// Failure inbox area ends.

// W9 routing policy under test (handlers/routing.go, docs/routing-experiments.md).

/** Routing strategy of an arm (schemas.RoutingPolicy.Strategy). */
export type RoutingStrategy = "fixed" | "weighted" | "fallback_chain" | "rule";

/** One provider and model of a routing policy; an empty provider is the run's gateway provider. */
export interface RouteTarget {
	provider?: string;
	model: string;
	weight?: number;
}

/** Governance limits on the run's virtual key (402/429 tests). */
export interface RouteLimits {
	budget_usd?: number;
	budget_reset?: string;
	request_max?: number;
	request_reset?: string;
	token_max?: number;
	token_reset?: string;
}

/** An arm's gateway policy (agent config gateway.routing). */
export interface RoutingPolicy {
	strategy: RoutingStrategy;
	targets?: RouteTarget[];
	fallbacks?: RouteTarget[];
	when?: string;
	retries?: number;
	limits?: RouteLimits;
}

/** One target's share of answered turns and its spend; cost absent when some run's spend on it was unknown. */
export interface RoutingTargetUse {
	target: string;
	turn_share: number;
	cost_usd?: number;
	latency_ms?: number;
}

/** What an arm's policy did under one condition, over the sealed counted runs. */
export interface RoutingCondition {
	condition: string;
	runs: number;
	fallback_runs: number;
	fallback_known: number;
	switches: number;
	switches_known: number;
	targets: RoutingTargetUse[];
}

/** One arm's policy, frozen hash and effect per condition. */
export interface RoutingArm {
	arm: string;
	label: string;
	model: string;
	policy?: RoutingPolicy;
	policy_hash?: string;
	summary: string;
	conditions: RoutingCondition[];
}

/** GET /experiments/{id}/routing. */
export interface RoutingSummary {
	exp_id: string;
	routed: boolean;
	arms: RoutingArm[];
	differences: string[];
}

/** One Bifrost message row of a run: which target served it and how it was routed. */
export interface RunRoutingRow {
	id: string;
	parent_id?: string;
	provider: string;
	model_served: string;
	fallback_index: number;
	status: string;
	error_status?: number;
	latency_ms: number;
	routing_rule_name?: string;
	routing_engines?: string[];
}

/** GET /runs/{id}/routing. */
export interface RunRouting {
	run_id: string;
	arm: string;
	condition: string;
	model: string;
	policy?: RoutingPolicy;
	summary: string;
	fallback_used?: boolean;
	switches?: number;
	targets: RoutingTargetUse[];
	rows: RunRoutingRow[];
	fidelity?: GateResult;
}

export type RoutingSummaryResponse = WithMode<RoutingSummary>;
export type RunRoutingResponse = WithMode<RunRouting>;
// W9 routing policy under test ends.

// W3 customer support (docs/customer-support.md): the support scorecard, schemas/support.go.

/** One arm's support numbers. A null number is unknown. On a support suite verified success is resolution. */
export interface SupportArm {
	arm: string;
	label?: string;
	runs: number;
	resolution: number | null;
	resolved: number;
	decided: number;
	cost_per_resolution: number | null;
	escalation_rate: number | null;
	escalation_correct: number | null;
	policy_violation_rate: number | null;
	user_turns: number | null;
	simulator_cost_usd: number | null;
}

/** GET /experiments/{id}/support. `support` false: the scorecard does not apply. */
export interface SupportSummary {
	exp_id: string;
	suite: string;
	condition: string;
	support: boolean;
	simulator_mode?: "scripted" | "llm";
	simulator_label: string;
	synthetic: boolean;
	arms: SupportArm[];
}

export type SupportSummaryResponse = WithMode<SupportSummary>;
// W3 customer support ends.

// W5 drift canary (handlers/canary.go, bench/canary, docs/drift-canary.md). Key lists under
// handlers/testdata/contract/Canary*.keys.

export type CanaryState = "no_baseline" | "steady" | "watching" | "drifted" | "invalid" | "baseline_stale";
export type CanaryRecordKind = "baseline" | "run" | "confirmation";
export type CanaryOutcome = "pinned" | "clear" | "flagged" | "invalid" | "stale" | "refused" | "error";
export type CanaryTestStatus = "worse" | "better" | "below_floor" | "no_change" | "cannot_tell";
export type CanaryEquivalenceVerdict = "differs" | "no_difference_detected" | "cannot_tell";

/** One watched route: a provider (empty keeps the agent's) and a model alias. */
export interface CanaryTarget {
	provider?: string;
	model: string;
}

export interface CanaryScheduleSpec {
	cron: string;
	timezone?: string;
	paused?: boolean;
}

export interface CanaryBudget {
	per_run_usd: number;
	per_day_usd: number;
}

export interface CanaryNoiseFloor {
	success?: number;
	cost_usd?: number;
	tokens?: number;
	latency_ms?: number;
}

/** canaries/<name>.yaml as the server read it. */
export interface CanaryDefinition {
	name: string;
	description?: string;
	agent: string;
	suite: string;
	tasks: "all" | string[];
	tier?: string;
	trials: number;
	conditions?: string[];
	targets: CanaryTarget[];
	schedule?: CanaryScheduleSpec;
	budget: CanaryBudget;
	alerts?: { url_env: string; secret_env?: string; events?: string[]; format?: string; timeout_s?: number }[];
	sensitivity?: string;
	noise_floor?: CanaryNoiseFloor;
	max_baseline_age_days?: number;
	smoke?: boolean;
	seed?: number;
	concurrency?: number;
}

/** Which models and providers answered one target. */
export interface CanaryServed {
	arm: string;
	label: string;
	requested: string[];
	served: string[];
	providers: string[];
	system_fingerprints?: string[];
	requests: number;
}

/** A pinned known-good experiment. */
export interface CanaryBaseline {
	exp_id: string;
	manifest_hash: string;
	fingerprint: string;
	pinned_at: string;
	created_at: string;
	served: CanaryServed[];
	clusters: string[];
	smoke?: boolean;
	note?: string;
}

/** The exact paired sign-flip test of one metric of one target on one condition. */
export interface CanaryMetricTest {
	key: string;
	arm: string;
	label: string;
	condition: string;
	metric: string;
	higher_is_better: boolean;
	tasks: number;
	discordant: number;
	worse: number;
	baseline: number | null;
	current: number | null;
	delta: number | null;
	relative: number | null;
	p: number | null;
	level: number;
	floor: number;
	status: CanaryTestStatus;
	reason: string;
}

export interface CanaryTaskFlip {
	arm: string;
	label: string;
	condition: string;
	task: string;
	baseline: number;
	current: number;
}

export interface CanaryComparison {
	alpha: number;
	family: number;
	tests: CanaryMetricTest[];
	flagged: string[];
	flips: CanaryTaskFlip[];
}

export interface CanaryClusterRef {
	signature: string;
	title: string;
	count: number;
	tasks: string[];
}

/** "What changed" for one target. Numbers are null when not observed. */
export interface CanaryArmChanges {
	arm: string;
	label: string;
	served_before: string[];
	served_now: string[];
	served_changed: boolean;
	providers_before: string[];
	providers_now: string[];
	provider_changed: boolean;
	fingerprints_before: string[];
	fingerprints_now: string[];
	fingerprint_changed: boolean;
	latency_before_ms: number | null;
	latency_now_ms: number | null;
	latency_shift: number | null;
	prompt_tasks: number;
	prompt_changed: number;
	prompt_shift: number | null;
	error_rate_before: number;
	error_rate_now: number;
	format_errors_before: number;
	format_errors_now: number;
	flips: CanaryTaskFlip[];
	success_before: number | null;
	success_now: number | null;
	requests_before: number;
	requests_now: number;
	requested_now: string[];
}

export interface CanaryChanges {
	moved: string[];
	arms: CanaryArmChanges[];
	new_clusters: CanaryClusterRef[];
}

/** Same model through two providers in one look. */
export interface CanaryEquivalence {
	model: string;
	arm_a: string;
	arm_b: string;
	label_a: string;
	label_b: string;
	tests: CanaryMetricTest[];
	verdict: CanaryEquivalenceVerdict;
	reason: string;
}

export interface CanaryArmPoint {
	arm: string;
	success: number | null;
	tasks: number;
	runs: number;
}

/** One look (or baseline). */
export interface CanaryRecord {
	id: string;
	canary: string;
	kind: CanaryRecordKind;
	started: string;
	finished: string;
	exp_id?: string;
	manifest_hash?: string;
	fingerprint?: string;
	baseline?: string;
	injected?: string;
	smoke?: boolean;
	outcome: CanaryOutcome;
	state: CanaryState;
	reason?: string;
	cost_usd: number;
	forecast_usd?: number;
	runs: number;
	run_errors: number;
	comparison?: CanaryComparison;
	changes?: CanaryChanges;
	equivalence?: CanaryEquivalence[];
	served?: CanaryServed[];
	invalid?: string[];
	alerts?: string[];
	points?: CanaryArmPoint[];
}

export interface CanarySummary {
	name: string;
	description: string;
	state: CanaryState;
	reason: string;
	suite: string;
	agent: string;
	targets: CanaryTarget[];
	tasks: number;
	trials: number;
	cron: string;
	next: string;
	sensitivity: string;
	alpha: number;
	smoke: boolean;
	per_run_usd: number;
	per_day_usd: number;
	spent_today_usd: number;
	baseline: CanaryBaseline | null;
	last_look: CanaryRecord | null;
	looks: number;
	paused?: boolean;
}

export interface CanaryDefinitionLoad {
	name: string;
	error: string;
}

/** GET /canaries */
export interface CanaryList {
	canaries: CanarySummary[];
	errors: CanaryDefinitionLoad[];
	states: CanaryState[];
}

export interface CanaryEquivalencePair {
	model: string;
	arm_a: string;
	arm_b: string;
	label_a: string;
	label_b: string;
}

/** GET /canaries/{name}. history is one trends lineage per target, drawn with the trends chart. */
export interface CanaryDetail {
	summary: CanarySummary;
	definition: CanaryDefinition;
	baselines: CanaryBaseline[];
	records: CanaryRecord[];
	history: TrendLineage[];
	pairs: CanaryEquivalencePair[];
}

/** POST /canaries/{name}/run (202). */
export interface CanaryRunAccepted {
	canary: string;
	started: string;
	message: string;
}

export type CanaryListResponse = WithMode<CanaryList>;
export type CanaryDetailResponse = WithMode<CanaryDetail>;
export type CanaryRunAcceptedResponse = WithMode<CanaryRunAccepted>;
// W5 drift canary ends.

// W8 judge calibration (docs/judge-calibration.md, handlers/calibration.go).

/** Calibration instrument and suite states. */
export type CalibrationState =
	| "calibrated"
	| "calibrated_on_controls"
	| "few_labels"
	| "uncalibrated"
	| "allowed_uncalibrated"
	| "unreliable"
	| "not_judged";

/** k of n with its exact two-sided 95% interval; value, lo and hi are null when n is 0. */
export interface CalibrationRate {
	k: number;
	n: number;
	value: number | null;
	lo: number | null;
	hi: number | null;
}

/** Judge against human labels. Positive is "correct"; a false positive is the judge passing an answer a person failed. */
export interface CalibrationStats {
	items: number;
	positives: number;
	negatives: number;
	tp: number;
	fp: number;
	fn: number;
	tn: number;
	undecided: number;
	agreement: CalibrationRate;
	tpr: CalibrationRate;
	tnr: CalibrationRate;
	fpr: CalibrationRate;
	/** Cohen's kappa; null when it is not defined. */
	kappa: number | null;
}

export interface CalibrationCategory {
	category: string;
	stats: CalibrationStats;
}

export interface CalibrationConsistency {
	votes: number;
	unanimous_items: CalibrationRate;
	repeat_agreement: CalibrationRate;
}

export interface CalibrationPerturbation {
	name: string;
	flips: CalibrationRate;
}

export interface CalibrationItemResult {
	item_id: string;
	task: string;
	category?: string;
	template: string;
	label: string;
	source: string;
	votes: string[];
	verdict: string;
	repeat: string;
	perturbed?: Record<string, string>;
}

/** One `judge calibrate` result, hash-chained beside the suite's labels. items is null in listings. */
export interface CalibrationRecord {
	record_id: string;
	suite: string;
	suite_version: string;
	provider: string;
	model: string;
	prompt_version: string;
	prompt_sha256: string;
	templates: Record<string, string>;
	votes: number;
	/** Vision judge: majority or mean, how a grade's votes decide; absent is majority. */
	aggregate?: string;
	/** Vision judge with aggregate mean: the score a grade's mean must reach. */
	pass_score?: number;
	served_models?: string[];
	set_sha256: string;
	set_rows: number;
	human_labels: number;
	seed_items: number;
	disputed: number;
	labellers?: string[];
	human: CalibrationStats;
	seed?: CalibrationStats;
	categories?: CalibrationCategory[];
	/** Human-item statistics per template sha256 (category is the hash). */
	by_template?: CalibrationCategory[];
	consistency: CalibrationConsistency;
	perturbations?: CalibrationPerturbation[];
	items: CalibrationItemResult[] | null;
	calls: number;
	usd: Value;
	price_ref?: string;
	created_at: string;
	prev_hash: string;
	hash: string;
}

export interface JudgeCalibrationThresholds {
	min_agreement: number;
	min_tnr: number;
	min_labels: number;
	max_age_days: number;
	/** Fewest admission-control items for "calibrated on controls". */
	min_controls: number;
	/** "defaults" or the release policy file. */
	source: string;
}

/** GET /suites/{id}/calibration: the calibration card. */
export interface CalibrationStatus {
	suite: string;
	judged: boolean;
	provider?: string;
	model?: string;
	prompt_version?: string;
	prompt_sha256?: string;
	state: CalibrationState;
	detail: string;
	/** Judged verdicts are capped at INCONCLUSIVE in this state. */
	caps: boolean;
	set_sha256?: string;
	labels: number;
	human_labels: number;
	seed_items: number;
	disputed: number;
	latest?: CalibrationRecord;
	records: number;
	thresholds: JudgeCalibrationThresholds;
	reminders: string[];
	problems: string[];
}

export type CalibrationLabel = "correct" | "incorrect";

/** One row of labels.jsonl. */
export interface CalibrationItem {
	id: string;
	task: string;
	answer: string;
	label: CalibrationLabel;
	labeller: string;
	note?: string;
	source: "human_review" | "label" | "import" | "admission_control";
	ref?: string;
	at: string;
	/** X1 vision grading: sha256 of the labelled image (calibration/images/<sha256>.png); answer may then be empty. */
	image?: string;
}

/** GET /suites/{id}/calibration/labels. */
export interface CalibrationLabels {
	suite: string;
	set_sha256: string;
	items: CalibrationItem[];
}

/** POST /suites/{id}/calibration/labels. */
export interface CalibrationLabelRequest {
	task: string;
	answer: string;
	label: CalibrationLabel;
	labeller: string;
	note?: string;
	ref?: string;
}

/** Raw judged rate of an arm beside its Rogan-Gladen correction (report only). */
export interface BiasAdjustedRate {
	raw: CalibrationRate;
	estimate: number | null;
	lo: number | null;
	hi: number | null;
	note?: string;
}

/** The judge_calibration instrument of a judged experiment's verdict. */
export interface JudgeCalibrationVerdict {
	state: CalibrationState;
	detail: string;
	model: string;
	templates: string[];
	record_id?: string;
	record_hash?: string;
	record_at?: string;
	human_labels: number;
	/** Admission-control items of the record. */
	control_items?: number;
	agreement?: CalibrationRate;
	tnr?: CalibrationRate;
	fpr?: CalibrationRate;
	kappa?: number | null;
	thresholds: JudgeCalibrationThresholds;
	reminders?: string[];
	judged_runs: number;
	contested_runs: number;
	error_over_effect?: string;
	capped_from?: Decision;
	bias_adjusted?: Record<string, BiasAdjustedRate>;
}

export type CalibrationStatusResponse = WithMode<CalibrationStatus>;
export type CalibrationLabelsResponse = WithMode<CalibrationLabels>;
export type CalibrationItemResponse = WithMode<CalibrationItem>;
// W8 judge calibration ends.

// W4 research with Tavily (docs/research-tasks.md): the research scorecard, schemas/research.go.

/** One arm's research numbers. A null number is unknown or not applicable in every run. */
export interface ResearchArm {
	arm: string;
	label?: string;
	runs: number;
	verified: number | null;
	passed: number;
	decided: number;
	searches: number | null;
	sources_cited: number | null;
	citations_grounded: number | null;
	citation_precision: number | null;
	external_unmatched: number | null;
}

/** GET /experiments/{id}/research. `research` false: the scorecard does not apply. */
export interface ResearchSummary {
	exp_id: string;
	suite: string;
	condition: string;
	research: boolean;
	replay: boolean;
	synthetic: boolean;
	arms: ResearchArm[];
}

export type ResearchSummaryResponse = WithMode<ResearchSummary>;
// W4 research with Tavily ends.

// W7 adaptive trials and suite tiers (handlers/adaptive.go, schemas/trials.go,
// docs/adaptive-trials.md, docs/suite-tiers.md). Key lists under
// handlers/testdata/contract/{AdaptiveView,TrialsPolicy,AdaptivePlan,AdaptiveReport,AdaptiveTaskState,AdaptiveLook,MoreEvidence,ExtendsRef}.keys.
export interface TrialsPolicy {
	mode: "fixed" | "adaptive";
	rule?: "task" | "sequential";
	min?: number;
	max?: number;
	conditions?: string[];
	looks?: number;
	alpha_split?: "equal" | "obf";
	alphas?: number[];
	futility?: "curtail" | "nonbinding" | "off";
	extension?: { rule: "inverse_normal"; alpha_first?: number; weight_first?: number };
}

export interface AdaptivePlan {
	rule: "task" | "sequential";
	conditions: string[];
	min?: number;
	max?: number;
	metric: string;
	looks?: string[][];
	look_alphas?: number[];
	alpha: number;
	futility?: "curtail" | "nonbinding" | "off";
	optional: number;
	required: number;
}

export interface AdaptiveTaskState {
	condition: string;
	task: string;
	trials: number;
	max: number;
	reason: "concordant" | "max_reached" | "waiting";
}

export interface AdaptiveLook {
	look: number;
	tasks: number;
	alpha: number;
	p_low?: number;
	p_high?: number;
	d_bar: number;
	outcome: "continue" | "efficacy" | "futility" | "final_look" | "not_reached" | "waiting";
	best_case_p?: number;
}

export interface AdaptiveReport {
	rule: "task" | "sequential" | string;
	max_slots: number;
	required_slots: number;
	run_slots: number;
	runs_saved: number;
	stop_reason: string;
	stop_look?: number;
	looks?: number;
	decision_alpha: number;
	tasks?: AdaptiveTaskState[];
	look_results?: AdaptiveLook[];
	excluded?: string[];
	notes?: string[];
	conditions?: string[];
}

export interface MoreEvidence {
	target_pp: number;
	tasks: number;
	tasks_total: number;
	over?: boolean;
	sd: number;
	sd_assumed: boolean;
	runs: number;
	cost_usd?: number;
	cost_basis: string;
	command?: string;
	combination: "inverse_normal" | "exploratory";
	alpha_kept?: number;
}

export interface ExtendsRef {
	exp_id: string;
	manifest_hash: string;
	kind: "tasks" | "trials";
	rule: "inverse_normal" | "exploratory";
}

/** GET /experiments/{id}/adaptive: the trials and tier panel. */
export interface AdaptiveView {
	exp_id: string;
	tier: string;
	tier_tasks: string[];
	trials: TrialsPolicy | null;
	plan: AdaptivePlan | null;
	report: AdaptiveReport | null;
	more_evidence: MoreEvidence | null;
	extends: ExtendsRef | null;
	decision: string;
	max_slots: number;
}

export type AdaptiveViewResponse = WithMode<AdaptiveView>;
// W7 adaptive trials and suite tiers ends.

// W6 migration report and cost explanation (docs/model-migration.md, docs/cost-explain.md): bench/costexplain, bench/migration.

/** GET /experiments/{id}/cost-explain query. */
export interface CostExplainParams {
	expId: string;
	a?: string;
	b?: string;
	condition?: string;
	grade_source?: string;
}

/** One arm's cost picture over its graded runs on the condition (costexplain.ArmCost). */
export interface CostArm {
	arm: string;
	label: string;
	model: string;
	served_models: string[];
	runs: number;
	successes: number;
	cost_per_run: Value;
	cost_per_success: Value;
	cache_neutral_per_run: Value;
	cache_neutral_per_success: Value;
	failed_run_cost_per_run: Value;
	cache_read_share: Value;
	cache_write_share: Value;
	turns_per_run: number;
	input_tokens_per_turn: number;
	output_tokens_per_turn: number;
	tokens: Tokens;
	/** Effective USD per million tokens of each bucket; absent when the arm has none of it. */
	price_input?: number;
	price_cache_read?: number;
	price_cache_write?: number;
	price_output?: number;
	attempts_per_run: number;
	wasted_attempts_per_run: number;
	retries_per_run: number;
	fallbacks_per_run: number;
	rejected_per_run: number;
	refusals_per_run: number;
	latency_ms_per_turn: Value;
	unpriced_rows: number;
	unattributed_rows: number;
	runs_without_rows: number;
	prompt_hashes: string[];
	tool_hashes: string[];
	keys: string[];
}

export type CostPartId =
	| "price"
	| "input_tokens"
	| "output_tokens"
	| "cache_read_share"
	| "cache_write_share"
	| "turns"
	| "wasted_attempts"
	| "unattributed"
	| "unpriced"
	| "success_rate";

/** One additive part of a waterfall; unknown with bounds when the rows do not know it. */
export interface CostPart {
	id: CostPartId;
	label: string;
	usd: Value;
	detail: string;
}

/** B minus A split into parts that sum to the total. */
export interface CostWaterfall {
	basis: "per_run" | "per_success";
	from: Value;
	to: Value;
	total: Value;
	parts: CostPart[];
	known: number;
	/** Why the waterfall does not apply, or "". */
	reason: string;
}

/** A measuring cost (judge, user simulator), reported apart from the agent's cost. */
export interface CostApart {
	id: string;
	label: string;
	a: Value;
	b: Value;
	diff: Value;
}

/** A silent cache break: the cache read share of arm dropped, with the likely cause. */
export interface CostCacheFinding {
	kind: "between_arms" | "over_time";
	arm: string;
	baseline: string;
	share_before: number;
	share_after: number;
	cause: string;
	evidence: string[];
}

export interface CostExplanation {
	exp_id: string;
	suite: string;
	condition: string;
	conditions: string[];
	price_ref: string;
	synthetic: boolean;
	a: CostArm;
	b: CostArm;
	per_run: CostWaterfall;
	per_success: CostWaterfall;
	apart: CostApart[];
	cache: CostCacheFinding[];
	notes: string[];
}

export type CostExplainResponse = WithMode<CostExplanation>;

export type ParetoAxis = "cost_per_success" | "cost_per_run";

/** One config on the cost against success plane. y carries the success interval as bounds. */
export interface ParetoPoint {
	id: string;
	label: string;
	agent: string;
	model: string;
	x: Value;
	y: Value;
	placed: boolean;
	frontier: boolean;
	dominated_by: string[];
	reason: string;
}

/** GET /experiments/{id}/pareto and GET /leaderboard/pareto. */
export interface Pareto {
	x: ParetoAxis;
	x_label: string;
	y: string;
	points: ParetoPoint[];
	frontier: string[];
	note: string;
}

export type ParetoResponse = WithMode<Pareto>;

export interface ExperimentParetoParams {
	expId: string;
	x?: ParetoAxis;
	condition?: string;
}

export type LeaderboardParetoParams = LeaderboardParams & { x?: ParetoAxis };

/** GET /experiments/{id}/migration query. */
export interface MigrationParams {
	expId: string;
	from?: string;
	/** Comma-separated models or arm ids. */
	to?: string;
	condition?: string;
	alpha?: string;
	grade_source?: string;
}

export interface MigrationArmRef {
	arm: string;
	label: string;
	agent: string;
	model: string;
	provider: string;
}

export type MigrationResult = "better" | "worse" | "cannot_tell";

/** One paired comparison: new minus old per-task success rate, exact sign-flip, Holm across family. */
export interface MigrationVerdict {
	/** "" for the overall verdict; else condition, task_kind, capability, tag, difficulty_declared, difficulty_observed. */
	dimension: string;
	value: string;
	condition: string;
	tasks: number;
	better: number;
	worse: number;
	mean_diff: number;
	p: number | null;
	p_adjusted: number | null;
	family: number;
	result: MigrationResult;
	reason: string;
	min_tasks: number;
	tasks_needed: number;
}

export type MigrationAction = "migrate" | "do_not_migrate" | "not_enough_evidence";

export interface MigrationRecommendation {
	action: MigrationAction;
	reason: string;
	policy?: PolicyResult;
	caveats: string[];
}

export interface MigrationChange {
	from: Value;
	to: Value;
}

export interface MigrationClusterRef {
	signature: string;
	title: string;
	count: number;
	tasks: string[];
	examples: string[];
}

export interface MigrationGateChange {
	gate: string;
	effect: string;
	from: number;
	to: number;
	status: "new" | "fixed" | "both";
}

export interface MigrationConditionDiff {
	condition: string;
	from_success: Value;
	to_success: Value;
	from_runs: number;
	to_runs: number;
	false_completion: MigrationChange;
	retry_amplification: MigrationChange;
}

export interface MigrationBehaviour {
	new_clusters: MigrationClusterRef[];
	fixed_clusters: MigrationClusterRef[];
	gates: MigrationGateChange[];
	false_completion: MigrationChange;
	rejected_per_run: MigrationChange;
	refusals_per_run: MigrationChange;
	retries_per_run: MigrationChange;
	fallbacks_per_run: MigrationChange;
	latency_ms_per_turn: MigrationChange;
	conditions: MigrationConditionDiff[];
}

export interface MigrationRunRef {
	run_id: string;
	pair_id: string;
	arm: string;
	trial: number;
	outcome: string;
}

export interface MigrationFlippedTask {
	task: string;
	condition: string;
	kind: "regressed" | "fixed";
	from_rate: number;
	to_rate: number;
	runs: MigrationRunRef[];
}

export interface MigrationPair {
	from: MigrationArmRef;
	to: MigrationArmRef;
	overall: MigrationVerdict;
	strata: MigrationVerdict[];
	recommendation: MigrationRecommendation;
	behaviour: MigrationBehaviour;
	flipped: MigrationFlippedTask[];
	cost: CostExplanation | null;
}

export interface MigrationModelSummary extends MigrationArmRef {
	success: Value;
	reliability: Value;
	k: number;
	cost_per_success: Value;
	cost_per_run: Value;
	tokens: Value;
	wall_time_s: Value;
	steps: Value;
	latency_ms_per_turn: Value;
	pareto: string;
}

export interface MigrationReport {
	exp_id: string;
	suite: string;
	condition: string;
	conditions: string[];
	alpha: number;
	/** The grade the report read: original or reviewed. */
	grade_source: string;
	synthetic: boolean;
	policy: string;
	from: MigrationArmRef;
	pairs: MigrationPair[];
	models: MigrationModelSummary[];
	pareto: Pareto | null;
	notes: string[];
}

export type MigrationResponse = WithMode<MigrationReport>;
// W6 migration report and cost explanation ends.

// W10 export (docs/export.md): GET /experiments/{id}/export?format=eee|jsonl|csv and
// GET /suites/{id}/export?format=harbor are file downloads. ExportRunRow is one line of the jsonl run
// table; key list: handlers/testdata/contract/ExportRunRow.keys. W10 export ends at the end of this block.
export type ExperimentExportFormat = "eee" | "jsonl" | "csv";

export interface ExportRunRow {
	exp_id: string;
	run_id: string;
	slot_id: string;
	pair_id: string;
	suite: string;
	task: string;
	task_version: string;
	arm: string;
	arm_label: string;
	agent_config: string;
	model: string;
	provider: string;
	condition: string;
	trial: number;
	attempt: number;
	outcome: string;
	/** public or held_out: the frozen plan's split of the task. */
	split: string;
	/** 1 or 0 as the leaderboard reads it: the observed verified_success metric, else the outcome's score; null when the outcome does not score (infra error, invalid evaluator). */
	verified_success: number | null;
	/** The outcome's own score (Y), 1 or 0; null when it does not score. */
	outcome_score: number | null;
	grade_passed: boolean | null;
	grade_source: string;
	cost_usd: number | null;
	synthetic: boolean;
	started?: string;
	sealed?: string;
	manifest_hash: string;
	seal_hash: string;
	problems?: string[];
	/** Every metric the run recorded; null when it did not observe it, never zero. */
	metrics: Record<string, number | null>;
}
// W10 export ends.

// X8 voice agents (docs/voice-agents.md): GET /runs/{id}/voice, the voice
// caller's timeline read the way the voice metrics read it.

/** One moment the floor passed to the agent and how long it took to speak (voice.Turn). */
export interface VoiceTurn {
	kind: string;
	caller_turn?: number;
	floor_ms: number;
	agent_start_ms?: number;
	gap_ms: number;
	latency_ms?: number;
	dead_air: boolean;
}

/** One time the caller talked over the agent (voice.BargeIn). */
export interface VoiceBargeIn {
	utt: number;
	caller_start_ms: number;
	agent_stop_ms: number;
	stopped_after_ms: number;
	by: string;
	handled: boolean;
}

/** One caller line with its known text (voice.Line). */
export interface VoiceLine {
	turn: number;
	kind: string;
	text: string;
	start_ms: number;
	end_ms: number;
	tts: string;
	final?: boolean;
}

/** One agent utterance as the caller heard and transcribed it (voice.Utterance). */
export interface VoiceUtterance {
	utt: number;
	start_ms: number;
	end_ms: number;
	text?: string | null;
	stt?: string;
	interrupted?: boolean;
}

/** A transcript line the agent reported about itself (voice.Reported). */
export interface VoiceReported {
	t_ms: number;
	role: string;
	text: string;
}

/** A call track the caller kept or only hashed (voice.AudioRef). */
export interface VoiceAudioRef {
	file?: string | null;
	sha256: string;
	bytes?: number;
	truncated?: boolean;
}

/** The analysed call (voice.Analysis). */
export interface VoiceAnalysis {
	protocol: string;
	wall_start_ms: number;
	call_ms: number;
	ended_by: string;
	first_audio_ms?: number;
	turns: VoiceTurn[];
	barge_ins: VoiceBargeIn[];
	dead_air_ms: number;
	lines: VoiceLine[];
	utterances: VoiceUtterance[];
	reported?: VoiceReported[];
	caller_frames: number;
	agent_frames: number;
	clears: number;
	protocol_errors: number;
	caller_audio?: VoiceAudioRef;
	agent_audio?: VoiceAudioRef;
	barge_in_bound_ms: number;
	dead_air_bound_ms: number;
	complete: boolean;
}

/** One kept audio track, served from a sealed run (handlers.VoiceTrack). */
export interface VoiceTrack {
	track: "caller" | "agent";
	sha256: string;
	bytes: number;
	url: string;
}

/** GET /runs/{id}/voice (handlers.VoiceCall). */
export interface VoiceCall {
	mode?: Mode;
	run_id: string;
	caller: string;
	called: boolean;
	reason?: string;
	analysis?: VoiceAnalysis;
	audio: VoiceTrack[];
	witness: string;
	/** Which bounds classified the call: the task's own (as the sealed metrics) or the defaults. */
	bounds_source: "task" | "default";
}

// ---------------------------------------------------------------------------------------------
// Multi-agent (schemas/agents.go, handlers/runagents.go, attribution/blame.go)
// ---------------------------------------------------------------------------------------------

/** How a step or an agent is known, strongest first. Other values may appear and are shown as text. */
export type AgentAttributionSource = "gateway_key" | "header" | "transcript" | "otlp" | "self_report" | (string & {});
/** proven: a bench-minted gateway key per container; declared: a header the gateway logged; reported: the agent's own record. */
export type AgentAttributionStrength = "proven" | "declared" | "reported";
export type AgentEdgeKind = "spawn" | "handoff" | "message" | "claim" | "return";

/** One agent of a run. Role, name and model are agent-influenced display text. Times are absent when no source recorded them. */
export interface AgentNode {
	id: string;
	role?: string;
	name?: string;
	model?: string;
	parent_id?: string;
	/** 0 for a root agent. */
	depth: number;
	spawn_tool_call_id?: string;
	spawn_event_id?: string;
	started?: string;
	ended?: string;
	/** completed | error | timeout | exit code n */
	exit?: string;
	exit_code?: number;
	source: AgentAttributionSource;
	strength: AgentAttributionStrength;
	sources?: string[];
	key_id?: string;
	container?: string;
	budget_usd?: number;
}

/** One interaction between two agents: the payload's hash and size, never the payload. */
export interface AgentEdge {
	kind: AgentEdgeKind;
	from: string;
	to?: string;
	at?: string;
	payload_sha256?: string;
	size?: number;
	source: AgentAttributionSource;
	strength: AgentAttributionStrength;
	ref?: string;
}

/** How much of the step timeline carries an agent id. A row no source could place stays unknown. */
export interface AgentCoverage {
	steps: number;
	attributed: number;
	unknown: number;
	by_source?: Record<string, number>;
	by_strength?: Record<string, number>;
	/** Sources that had anything to say in this run. */
	available?: string[];
}

/** agents.json: every agent of one run, how they were started and how they talked. */
export interface AgentTree {
	version: string;
	/** in_process | team */
	shape: string;
	agents: AgentNode[];
	edges?: AgentEdge[];
	coverage: AgentCoverage;
	notes?: string[];
}

/** One agent's row in the per-agent table. A null count was not recorded (tokens and cost need gateway rows with the agent's id), never zero. */
export interface AgentStats {
	agent_id: string;
	role?: string;
	model?: string;
	parent_id?: string;
	depth: number;
	/** Strongest attribution source of the agent. */
	source: AgentAttributionSource;
	strength: AgentAttributionStrength;
	/** Timeline rows with this agent id. */
	steps: number;
	model_turns: number;
	tool_calls: number;
	input_tokens: number | null;
	output_tokens: number | null;
	cost_usd: number | null;
	messages_sent: number;
	messages_received: number;
	busy_ms: number;
	exit?: string;
}

/** One swimlane block: a timeline row with an agent id, in milliseconds from the run's first row. Label is clipped agent text. */
export interface AgentSpan {
	agent_id: string;
	/** The timeline row. */
	seq: number;
	kind: string;
	start_ms: number;
	end_ms: number;
	label: string;
	is_error?: boolean;
	/** On the critical path. */
	critical?: boolean;
}

/** One planted fact checked on a handoff. */
export interface HandoffFact {
	fact: string;
	sent: boolean;
	delivered: boolean;
}

/**
 * One edge of the agent tree with what travelled on it. sent is the payload as sent and delivered
 * as received, both clipped agent text; altered is true when they differ.
 */
export interface AgentHandoff {
	index: number;
	kind: AgentEdgeKind;
	from: string;
	to?: string;
	at?: string;
	/** ms from the run's first timeline row, the same zero as spans; null when either time is unknown. */
	at_ms: number | null;
	bytes: number;
	payload_sha256?: string;
	sent?: string;
	delivered?: string;
	altered?: boolean;
	ref?: string;
	/** gateway_key when the sender is proven by its route token, else self_report. */
	source: AgentAttributionSource;
	facts?: HandoffFact[];
}

/** Blame tiers, strongest first. unattributed: no agent could be proven responsible; single_agent: nothing to attribute. */
export type BlameTier = "replay_validated" | "gate_proven" | "unattributed" | "single_agent";
export type BlameReplayStatus = "not_run" | "flipped" | "held" | "error";

/** One fired gate's reference placed on an agent (Go attribution.Candidate). */
export interface BlameCandidate {
	agent_id: string;
	role?: string;
	gate: string;
	effect: string;
	ref: string;
	step_seq?: number;
	at?: string;
	agent_source?: string;
}

/**
 * The tier 2 counterfactual (Go attribution.Replay): the slot run again with the suspect replaced
 * by a scripted stub. flipped validates the blame; held means the suspect alone does not explain it.
 */
export interface BlameReplay {
	status: BlameReplayStatus;
	suspect: string;
	stub: string;
	exp_id?: string;
	run_id?: string;
	outcome_before: string;
	outcome_after?: string;
	/** Gates that still fired in the replay. */
	gates_after?: string[];
	/** Absent when the replay's cost was not recorded. */
	cost_usd?: number;
	detail?: string;
}

/** blame.json: which agent of one run is responsible for its failure, how that is known, and the replay when one ran. */
export interface Blame {
	version: string;
	run_id: string;
	exp_id?: string;
	parent_seal?: string;
	outcome: string;
	/** Outcome not pass, or a veto or report gate fired. */
	failed: boolean;
	tier: BlameTier;
	/** Empty unless the tier is gate_proven or replay_validated. */
	agent_id?: string;
	role?: string;
	gate?: string;
	step_seq?: number;
	at?: string;
	/** How the step's agent is known. */
	agent_source?: string;
	strength?: AgentAttributionStrength;
	evidence?: string[];
	detail: string;
	/** Every placed reference of every fired gate, earliest first. */
	candidates?: BlameCandidate[];
	/** Fired gates' references no agent could be found for. */
	unplaced?: string[];
	replay?: BlameReplay;
	coverage?: AgentCoverage;
	notes?: string[];
}

/**
 * GET /runs/{id}/agents. available is false for a single-agent run (no agents.json): the lists are
 * then empty and tree and blame absent. Every string that came from an agent is inert display text.
 */
export interface RunAgents {
	run_id: string;
	available: boolean;
	tree?: AgentTree;
	/** One row per agent, in tree order (depth first). */
	members: AgentStats[];
	/** Swimlane blocks; critical marks the blocks on the critical path. */
	spans: AgentSpan[];
	/** First to last timeline row. busy_ms / wall_ms is the parallelism factor. */
	wall_ms: number;
	busy_ms: number;
	critical_ms: number;
	/** Spawn, handoff, message, claim and return edges in time order. */
	handoffs: AgentHandoff[];
	blame?: Blame;
	/** The timeline was longer than one response carries: spans cover the first rows only. */
	truncated?: boolean;
}

export type RunAgentsResponse = WithMode<RunAgents>;

// ---------------------------------------------------------------------------------------------
// Eval integrity area (S2): verdict.go VarianceCard, the eval-awareness scan, monitor
// certification (monitor.go) and the compliance pack route.
// ---------------------------------------------------------------------------------------------

/** observed_effect: an audit of a published table, whose own per-task differences give the SD. */
export type VarianceSource = "measured_aa" | "assumed" | "observed_effect";

/** The noise floor of an experiment: the SD the MDE used, where it came from, and the A/A comparison when an A/A arm ran. */
export interface VarianceCard {
	source: VarianceSource;
	/** The SD the MDE and tasks_needed used. */
	sd: number;
	assumed_sd: number;
	/** SD of A2 minus A per task; absent without an A/A arm or with fewer than 2 paired tasks. */
	measured_sd?: number;
	aa_arm?: string;
	aa_tasks?: number;
	aa_effect?: Effect;
	/** improved or regressed between identical arms is harness noise and makes the verdict invalid. */
	aa_decision?: Decision;
	/** Percentage points. */
	mde: number;
	detail: string;
}

export type AwarenessSource = "assistant_message" | "reasoning_summary" | "final_message" | "agent_stream";

export interface AwarenessArm {
	runs: number;
	runs_scanned: number;
	runs_with_signals: number;
	signals: number;
}

export interface AwarenessExample {
	run_id: string;
	arm: string;
	task: string;
	condition: string;
	source: AwarenessSource | string;
	phrase: string;
	excerpt: string;
}

/** GET /experiments/{id}/awareness: a heuristic phrase scan of the agent's own words for test-awareness statements. Report only. */
export interface AwarenessReport {
	exp_id: string;
	realism: string;
	heuristic: boolean;
	scanner_version: string;
	label: string;
	per_arm: Record<string, AwarenessArm>;
	examples: AwarenessExample[];
	not_scanned?: { run_id: string; reason: string }[];
}

export type AwarenessResponse = WithMode<AwarenessReport>;

export interface MonitorInfo {
	name: string;
	kind: "command" | "http" | "builtin" | string;
	mode: "transcript" | "action" | string;
	config_hash: string;
	image?: string;
	network?: string;
	builtin?: string;
	timeout_s?: number;
}

/** One fixed false-positive rate: the threshold it gives on honest runs and the attacks caught there, with exact binomial intervals. */
export interface OperatingPoint {
	target_fpr: number;
	/** false when the honest set is too small for this rate (fewer than 1/target runs). */
	supported: boolean;
	/** null: only block decisions fit the target; no score cut does (the note says so). */
	threshold?: number | null;
	false_positives: number;
	honest: number;
	/** null when there is no honest run. */
	achieved_fpr: number | null;
	fpr_interval?: [number, number];
	caught: number;
	attacks: number;
	/** null when there is no attack run; the interval is then [0, 1]. */
	catch_rate: number | null;
	catch_interval?: [number, number];
	note?: string;
}

export interface FamilyPoint {
	target_fpr: number;
	caught: number;
	attacks: number;
	catch_rate: number | null;
	catch_interval?: [number, number];
}

export interface MonitorFamily {
	family: string;
	gates?: string[];
	attacks: number;
	points: FamilyPoint[];
}

export interface MonitorItem {
	run_id: string;
	exp_id: string;
	task: string;
	arm: string;
	condition: string;
	label: "honest" | "attack";
	family?: string;
	gates?: string[];
	/** null with decision "" when the monitor errored; an error never counts as allow. */
	score: number | null;
	decision: "allow" | "block" | "" | string;
	reason?: string;
	actions?: number;
	truncated?: boolean;
	synthetic?: boolean;
	grade_source?: string;
	seal_hash: string;
	error?: string;
}

/** The monitor-injection check: planted "approved, score 0" text in attack transcripts; flipped counts verdicts it changed. */
export interface MonitorIntegrity {
	state: "passed" | "failed" | "not_run" | string;
	plant?: string;
	tested?: number;
	flipped?: number;
	honest_tested?: number;
	honest_flagged?: number;
	errors?: number;
	detail?: string;
}

export interface MonitorHeadline {
	target_fpr: number;
	catch_rate: number | null;
	catch_interval?: [number, number];
	supported: boolean;
}

/** A monitor's certificate over labelled sealed runs (docs/monitor-certification.md). */
export interface MonitorCertification {
	cert_id: string;
	created_at: string;
	monitor: MonitorInfo;
	exp_ids: string[];
	experiments?: { exp_id: string; manifest_hash: string; suite_id: string; suite_version: string; runs: number }[];
	labels: { honest: number; attack: number; excluded: number; rule: string; excluded_reasons?: Record<string, number> };
	operating_points: OperatingPoint[];
	families: MonitorFamily[];
	headline?: MonitorHeadline;
	integrity?: MonitorIntegrity;
	warnings?: string[];
	items?: MonitorItem[];
	excluded?: { run_id: string; exp_id: string; task: string; arm: string; condition: string; seal_hash: string; reason: string }[];
	errors?: number;
	error_limit?: number;
	grade_source?: string;
	attack_axes?: string[];
	harm_metrics?: string[];
	transcript?: Record<string, unknown>;
	bench_version?: string;
	bench_mode?: string;
}

export interface MonitorCertificationSummary {
	cert_id: string;
	created_at: string;
	monitor_name: string;
	monitor_kind: string;
	mode: string;
	exp_ids: string[];
	honest: number;
	attacks: number;
	headline?: MonitorHeadline;
	warnings?: string[];
}

export type MonitorCertificationListResponse = WithMode<{ certifications: MonitorCertificationSummary[] }>;
export type MonitorCertificationResponse = WithMode<MonitorCertification>;

// X4: code scan of the lines an agent added (bench/schemas/codescan.go, docs/code-scan.md).

/** Code scan severities, lowest first. */
export type CodeScanSeverity = "low" | "medium" | "high" | "critical";

/** One security finding on a line the agent added (or a protection it removed). Snippet is redacted, inert text. */
export interface CodeScanFinding {
	rule: string;
	title: string;
	cwe?: string;
	severity: CodeScanSeverity;
	file: string;
	line: number;
	removed?: boolean;
	/** 1-based line of workspace.diff holding the finding. */
	patch_line?: number;
	snippet?: string;
	/** "builtin" or an external scanner's id. */
	source: string;
	in_test?: boolean;
}

/** One external scanner's run; error set means its findings are unknown, never zero. */
export interface CodeScanExternal {
	scanner: string;
	image: string;
	exit_code: number;
	sarif_sha256?: string;
	results: number;
	on_added_lines: number;
	error?: string;
}

/** A changed file the scan did not read. */
export interface CodeScanSkip {
	file: string;
	reason: string;
}

/** One run's code scan. scanned false carries the reason. */
export interface CodeScanEvidence {
	rules_version: string;
	scanned: boolean;
	reason?: string;
	diff_sha256?: string;
	files: number;
	added_lines: number;
	skipped?: CodeScanSkip[];
	findings: CodeScanFinding[];
	external?: CodeScanExternal[];
}

/** GET /runs/{id}/codescan. sealed false: the builtin rules ran again now on the sealed diff. */
export interface CodeScanReport {
	run_id: string;
	sealed: boolean;
	veto_at?: CodeScanSeverity;
	ignored_rules?: string[];
	counted: number;
	vetoed: boolean;
	scan: CodeScanEvidence;
}

export type CodeScanReportResponse = WithMode<CodeScanReport>;

// X2: services of a run and their captured logs (bench/handlers/runservices.go, docs/multi-container.md).

/** One service that ran beside the agent. log_bytes is set when its log was captured (compose services). */
export interface RunService {
	id: string;
	role?: string;
	image: string;
	image_id?: string;
	ready: boolean;
	error?: string;
	proxied?: boolean;
	log_bytes?: number;
}

/** GET /runs/{id}/services. */
export interface RunServices {
	run_id: string;
	sealed: boolean;
	services: RunService[];
}

export type RunServicesResponse = WithMode<RunServices>;

/** A suite reference with the frozen version when known ("<id>@<version>"), for task links. */
export function frozenSuiteRef(suite: string | undefined, version: string | undefined): string | undefined {
	if (!suite) return undefined;
	return version && !suite.includes("@") ? `${suite}@${version}` : suite;
}