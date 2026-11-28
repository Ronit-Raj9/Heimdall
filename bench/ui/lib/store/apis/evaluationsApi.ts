import { withOrgParam } from "./orgParam";
import type { DatasetComparison } from "@/lib/types/bench";
import { benchApi } from "./benchApi";
import type { FailurePatterns } from "./failurePatternsApi";

export type { DatasetBenchmark, DatasetComparison } from "@/lib/types/bench";

/**
 * Agent-first benchmarking (audit/AGENTFIRST_CONTRACT.md, lanes A01-A19): capabilities, agent
 * versions, evaluations, packs, estimates, report cards and the agent leaderboard. The types mirror
 * the Go JSON exactly; other UI lanes import from here.
 */

// ---------------------------------------------------------------------------------------------
// 1. Capabilities (A01)
// ---------------------------------------------------------------------------------------------

export type Capability = "shell" | "tools" | "mcp" | "sessions" | "browser" | "desktop" | "voice" | "gpu";
export const CAPABILITIES: Capability[] = ["shell", "tools", "mcp", "sessions", "browser", "desktop", "voice", "gpu"];

export interface CompatibilityBenchmark {
	id: string;
	ok: boolean;
	requires: Capability[];
	missing: Capability[];
	reason: string;
	/** The agent runs only some tasks: how many are skipped and why. */
	note?: string;
}

/** GET /agents/{id}/compatibility */
export interface Compatibility {
	agent: string;
	capabilities: Capability[];
	benchmarks: CompatibilityBenchmark[];
}

// ---------------------------------------------------------------------------------------------
// 2. Agent versions (A06)
// ---------------------------------------------------------------------------------------------

export interface AgentVersion {
	version: number;
	hash: string;
	created_at: string;
	created_by: string;
	created_by_name?: string;
	note: string;
	/** Runs made with this version; present on the list endpoint. */
	runs?: number;
	/** The Files API version of agents/<id>.yaml this snapshot was taken from (GET /project/versions/agents/<id>.yaml). */
	file_version?: string;
	file_sha256?: string;
}

export interface AgentVersionCreated extends AgentVersion {
	created: boolean;
}

/** GET /agents/{id}/versions */
export interface AgentVersionList {
	agent: string;
	versions: AgentVersion[];
	/** The version holding the saved config: what pull requests are compared with. */
	live?: number;
	/** True when the agent has no settings file, so it has no versions. */
	no_file?: boolean;
}

/** GET /agents/{id}/versions/{n} */
export interface AgentVersionDetail {
	version: number;
	hash: string;
	yaml: string;
}

// ---------------------------------------------------------------------------------------------
// 3. Evaluations (A02, A03, A07)
// ---------------------------------------------------------------------------------------------

export type EvaluationMode = "score" | "versions" | "models" | "opponent";
export type EvaluationStatus = "queued" | "running" | "done" | "failed" | "cancelled";
/** "clean" or a fault id from GET /faults (model-down, tool-rate-limit, ...). */
export type EvaluationFault = string;

export type FaultLayer = "model" | "tool" | "instructions" | "compound";

/** GET /faults item: a fault an evaluation can add besides clean. */
export interface FaultCatalogItem {
	id: string;
	label: string;
	help: string;
	layer: FaultLayer | string;
	group: "Model" | "Tools" | "Instructions" | "Combined" | string;
}
export type PackId = "coding" | "terminal" | "support" | "memory" | "research" | "voice" | "safety" | "all-compatible";

export interface EvaluationArm {
	label: string;
	agent: string;
	/** The version that ran; set by the server at launch when the arm asked for the latest. */
	version?: number;
	model: string;
	provider?: string;
	/** True when the arm asked for the latest version and `version` is the one that was latest at launch. */
	latest_at_launch?: boolean;
}

export interface EvaluationSpec {
	name: string;
	mode: EvaluationMode;
	/** 1 arm for score, 2 for A/B. */
	arms: EvaluationArm[];
	/** Suite ids; or Pack. */
	benchmarks: string[];
	pack?: PackId | string;
	/** Default 10 (the Quick look preset), admitted tasks only. */
	tasks_per_benchmark: number;
	/** Repeats per task, default 1. */
	trials: number;
	/** "clean" always; plus "model-down", "broken-tool", "misleading-instruction". */
	faults: EvaluationFault[];
	budget_usd?: number;
	/** Omitted or "public"; "held_out" is an explicit held-out evaluation. */
	split?: "public" | "held_out";
	/** Checked automatically when the evaluation finishes. */
	gate?: EvaluationGate;
	/** The connected worker (its pool) the evaluation runs on; unset is the org's default worker, "shared" the shared worker. */
	worker?: string;
	/** Run each benchmark's tasks one after another with the agent's memory kept. */
	stream?: { memory: "carry" | "fresh"; order?: "suite" | "seeded"; replay?: number; threshold?: number };
	/** Tell the agent its budget and deadline (BENCH_BUDGET_USD, BENCH_DEADLINE). */
	disclose_budget?: boolean;
	/** Runs of one benchmark that go at once, 1 to 16; unset is 4. The org's worker slots still bound it. */
	concurrency?: number;
	/** The size preset the wizard started from; the numbers above are what runs. first_look (lane Z01): 1 benchmark, 5 tasks, 1 trial, 5 at a time; too small for a verdict. */
	preset?: EvaluationPresetId | "first_look";
	/** Bifrost cache on vs off (score mode): the server adds a second arm that is the same agent with Bifrost's semantic cache on. */
	bifrost_cache?: boolean;
	/** How arms reach the tasks' MCP tool servers: unset or "direct" through the tool proxy, "bifrost" through Bifrost's MCP gateway, "bifrost_code_mode" the same with Code Mode. */
	mcp_route?: EvaluationMCPRoute;
	/** Provider failover: a second arm with Bifrost fallbacks, run under an outage of the primary model. One arm only. */
	failover?: EvaluationFailover;
}

/** Bifrost fallbacks for the agent's model, on the same provider; tried in order when the primary fails. */
export interface EvaluationFailover {
	enabled: boolean;
	fallbacks: { provider?: string; model: string }[];
	outage_status?: number;
}

/** The report card's "Provider failover" section (reportcard Report.failover). */
export interface FailoverArmStats {
	calls: number;
	errors: number;
	fallback_served: number;
	mean_latency_ms: number;
	cost_usd: number;
	cost_known: boolean;
	task_success: boolean;
	success_rate?: number;
}

export interface FailoverReport {
	clean: FailoverArmStats;
	failover: FailoverArmStats;
	no_failover: FailoverArmStats;
	failover_worked: boolean;
	/** At least one call or run was answered by the fallback model; absent in reports made before it existed. */
	fallback_reached?: boolean;
	extra_latency_ms: number;
	cost_change_usd?: number;
	success_with_failover: boolean;
	success_without_failover: boolean;
	summary: string;
	per_run?: boolean;
}

export type EvaluationPresetId = "quick" | "standard" | "thorough";

/** The estimate request: a spec, plus `presets: true` to also get the three preset sizes. */
export type EstimateRequest = EvaluationSpec & { presets?: boolean };

export type EvaluationMCPRoute = "direct" | "bifrost" | "bifrost_code_mode";

/** What an evaluation must show to pass. Scores are fractions (0 to 1). */
export interface EvaluationGate {
	/** The version compared with; 0 or absent: the agent's live version (the last one released). */
	baseline_version?: number;
	/** Lowest score per capability ("all" is every benchmark pooled). */
	min_score?: Record<string, number>;
	/** Largest score drop from the baseline on any capability (0.05 is 5 points). */
	max_regression?: number;
	/** Count min_score on a judge-graded benchmark whose judge is not calibrated. */
	allow_uncalibrated_judge?: boolean;
	/** An inconclusive gate blocks a release like a failed one. */
	fail_on_inconclusive?: boolean;
	/** Rules for one task segment (tasks whose metadata key has value); checks name it "key=value". */
	slices?: EvaluationGateSlice[];
}

/** A gate rule on one segment: lowest score and/or largest drop from the baseline, as fractions. */
export interface EvaluationGateSlice {
	key: string;
	value: string;
	min_score?: number;
	max_regression?: number;
}

export type GateState = "pass" | "fail" | "inconclusive" | "no_baseline";

export interface GateCheck {
	capability: string;
	rule: "min_score" | "max_regression" | string;
	threshold: number;
	observed: number;
	baseline?: number;
	pass: boolean;
	summary: string;
	/** The rule could not decide (pass is false). */
	inconclusive?: boolean;
	/** Paired tasks compared (max_regression). */
	tasks?: number;
	/** Candidate minus baseline on the paired tasks, with its 95% paired bootstrap interval. */
	change?: number;
	change_low?: number;
	change_high?: number;
	/** Smallest change the paired tasks can detect (alpha 0.05, power 0.8). */
	mde?: number;
	/** What was left out: tasks only one side ran, another benchmark version. */
	notes?: string[];
}

export interface GateBaseline {
	agent: string;
	version?: number;
	arm?: string;
	evaluation_id?: string;
	live: boolean;
}

export interface EvaluationGateResult {
	state: GateState | string;
	agent: string;
	candidate: string;
	candidate_version?: number;
	baseline?: GateBaseline;
	checks: GateCheck[];
	summary: string;
	decided_at: string;
	/** Set when the baseline comes from another evaluation: did the benchmark change since. */
	dataset?: DatasetComparison;
}

export interface EvaluationSkippedTask {
	task: string;
	reason: string;
}

export interface EvaluationChild {
	benchmark: string;
	experiment_id: string;
	status: string;
	skipped?: string;
	/** Why it was skipped, as a fixed code (G1), for example "no_worker". Older servers send none. */
	cause?: string;
	next_run_usd?: number;
	/** Tasks left out of this benchmark alone (quarantined); the rest runs. */
	skipped_tasks?: EvaluationSkippedTask[];
	/** Condition id to fault (or "attack"), fixed at launch. */
	fault_of?: Record<string, string>;
	/** Faults chosen for the evaluation that do not apply to this benchmark, with the reason. */
	faults_skipped?: { fault: string; reason: string }[];
	/** The spend cap this benchmark runs under (its share of the budget, or the $5 default). */
	budget_usd?: number;
	/** Modeled spend of its runs so far. */
	spent_usd?: number;
	/** The spend cap stopped it with runs left; `not_started` counts them. */
	budget_stopped?: boolean;
	not_started?: number;
	/** The benchmark version this child ran, fixed at launch. */
	suite_version?: string;
	lock_hash?: string;
	tasks?: string[];
	task_hashes?: Record<string, string>;
}

export interface Evaluation {
	id: string;
	org?: string;
	spec: EvaluationSpec;
	status: EvaluationStatus;
	children: EvaluationChild[];
	created_at: string;
	created_by?: string;
	/** The schedule that started this evaluation, if any. */
	schedule?: string;
	/** Set once the evaluation is done and its spec has a gate. */
	gate_result?: EvaluationGateResult;
	/** Run control: the evaluation whose failures this one reruns (exploratory, never counted). */
	rerun_of?: string;
	/** Run control: every running benchmark is paused. */
	paused?: boolean;
	result?: EvaluationResult;
	launch?: { started: number; of: number; current?: string };
	failed_runs?: number;
	problem?: string;
}

/** One arm of a stored evaluation result: its clean score and runs over every benchmark. */
export interface EvaluationArmResult {
	label: string;
	agent: string;
	version: number;
	model: string;
	score: number;
	runs: number;
}

/** The headline numbers stored once an evaluation finished (N161); cost_usd is missing when any arm's spend is unknown. */
export interface EvaluationResult {
	arms: EvaluationArmResult[];
	cost_usd?: number;
	at: string;
}

/** GET /evaluation-packs item. */
export interface Pack {
	id: PackId | string;
	name: string;
	benchmarks: string[];
	capability: string;
}

export interface Range {
	low: number;
	high: number;
}

export interface EstimateBenchmark {
	benchmark: string;
	runs?: number;
	est_cost_usd?: Range & { known?: boolean; reason?: string };
	est_minutes?: Range & { known?: boolean };
	skipped?: string;
	notes?: string[] | null;
	faults_skipped?: { fault: string; reason: string }[] | null;
	/** Some setup has no runs of its own: cost and time come from other agents' library runs. */
	from_library?: boolean;
	[key: string]: unknown;
}

/** Smallest detectable change in percentage points; null when it cannot be decided. */
export interface EstimatePower {
	per_benchmark: {
		benchmark: string;
		tasks: number;
		decidable: boolean;
		mde_pp: number | null;
	}[];
	overall: { tasks: number; mde_pp: number | null };
	sd_source: "assumed" | "measured" | string;
	advice: string;
}

/** POST /evaluations/estimate */
export interface Estimate {
	runs: number;
	est_cost_usd: Range & { known: boolean; reason?: string };
	est_minutes: Range & { known?: boolean };
	per_benchmark: EstimateBenchmark[];
	power?: EstimatePower;
	notes?: string[] | null;
	/** Arm models the price snapshot does not list: no cost is estimated for them. */
	unpriced_models?: string[];
	/** The spend cap that applies, the suggested one, and the runs a smaller cap leaves unstarted. */
	budget?: EstimateBudget;
	/** Runs that go at once: per benchmark, over the whole evaluation, and the org's worker slots (0: not bounded). */
	slots?: { concurrency: number; org_slots: number; parallel: number; worker_slots?: number };
	/** A first run: the estimate comes from library runs of other agents. */
	from_library?: boolean;
	/** The preset sizes of the same setup, when asked with `presets: true`. */
	presets?: EstimatePreset[] | null;
	/** Where the cost came from; "list_price" for an arm with no run history (server wiring). */
	estimate_basis?: string;
}

export type BudgetFit = "fits" | "may_exceed" | "exceeds" | "unknown";

/** handlers.EvalBudget */
export interface EstimateBudget {
	budget_usd: number;
	per_benchmark_usd: number;
	/** No budget was given: the per-benchmark default applies. */
	default: boolean;
	default_per_benchmark_usd: number;
	/** Covers the high cost estimate with headroom, at least the default, at most `max_usd`. */
	suggested_usd: number;
	max_usd: number;
	fit: BudgetFit | string;
	runs_skipped_low: number;
	runs_skipped_high: number;
	note?: string;
}

/** handlers.EvalPresetEstimate */
export interface EstimatePreset {
	id: EvaluationPresetId;
	label: string;
	summary: string;
	tasks_per_benchmark: number;
	trials: number;
	faults: string[];
	runs: number;
	est_cost_usd: Range & { known: boolean; reason?: string };
	est_minutes: Range & { known?: boolean };
	/** Smallest change it can detect overall, in points; null when it cannot decide. */
	mde_pp: number | null;
	mde_per_benchmark_pp: number | null;
	from_library?: boolean;
}

/** POST /evaluations/{id}/raise-budget */
export interface RaiseBudgetRequest {
	id: string;
	/** The new budget of the whole evaluation, in USD. */
	budget_usd: number;
}

// ---------------------------------------------------------------------------------------------
// 4. Report card (A04, A05)
// ---------------------------------------------------------------------------------------------

export type ReportCapabilityId = "terminal" | "coding" | "memory" | "support" | "research" | "voice" | "safety" | "team" | "gpu";

export interface ReportMetrics {
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
	tasks?: number;
	/** Sum over the runs; null when any run's cost is not known (an unpriced model is never $0). */
	cost_usd?: number | null;
	/** Runs whose cost is known. */
	cost_runs?: number;
	/** Why the other runs' cost is not known. */
	cost_note?: string;
	/** Sum over the runs; null when any run's token count is not known. */
	tokens?: number | null;
	token_runs?: number;
	steps?: number;
	tool_calls?: number;
	wall_s?: number;
	/** Runs left out of the score by reason: out_of_memory, infra_error, invalid_evaluator, unknown. */
	excluded?: Record<string, number>;
	/** Process scores against the task's reference tool path. Report only: never part of the score. */
	process?: ReportProcess;
}

/** How the agent's tool calls compare with the task's reference path (reportcard.Metrics.process). */
export interface ReportProcess {
	runs: number;
	/** Share 0..1 of runs whose tool path matched the reference. */
	tool_match: number | null;
	/** Median agent tool calls divided by reference tool calls; 1.0 is the same as the reference. */
	step_ratio: number | null;
	/** Median repeated identical calls per run. */
	redundant_calls: number | null;
}

export interface ReportArm {
	label: string;
	agent: string;
	version?: number;
	/** The evaluation asked for the latest version; `version` is the one that was latest at launch. */
	latest_at_launch?: boolean;
	model: string;
}

/** A/B verdict on one benchmark (bench/verdictab). Winner is an arm label, "tie" or "unclear". */
export interface ReportVerdict {
	winner: string;
	delta: number;
	significant: boolean;
	summary: string;
	p_value?: number;
	/** p_value after Holm across the evaluation's benchmarks; significant is read from this. */
	p_value_holm?: number;
	ci_low?: number;
	ci_high?: number;
	tasks?: number;
	/** 95% paired bootstrap interval of delta. */
	boot_low?: number;
	boot_high?: number;
	/** Smallest change this many paired tasks can detect. */
	mde?: number;
	/** Taken back because the benchmark's judge is not calibrated; capped_from is the winner before. */
	judge_capped?: boolean;
	capped_from?: string;
}

/** Calibration of the model judge that grades a benchmark (G4-06). */
export interface ReportJudgeState {
	state: string;
	/** "human-calibrated", "calibrated on controls", "few human labels (12 of 30)", "judge not calibrated", ... */
	label: string;
	/** Verdicts and min_score gates may use it. */
	trusted: boolean;
	model?: string;
	human_labels: number;
	control_items: number;
	min_labels?: number;
	detail?: string;
}

export interface ReportCapabilityRow {
	capability: ReportCapabilityId | string;
	benchmarks: string[];
	arms: Record<string, ReportMetrics>;
}

export interface ReportBenchmarkRow {
	benchmark: string;
	capability: ReportCapabilityId | string;
	experiment_id: string;
	arms: Record<string, ReportMetrics>;
	verdict?: ReportVerdict;
	/** Repeatability per arm on clean runs. */
	reliability_card?: Record<string, ReportReliabilityCard | null>;
	/** The benchmark version the evaluation ran. */
	suite_version?: string;
	lock_hash?: string;
	/** Set when a model judge grades the benchmark. */
	judge?: ReportJudgeState;
	/** Per arm, runs a safety check threw out of the score (fix wave 3, N215). */
	thrown_out?: Record<string, ReportThrownOut>;
}

/** Runs thrown out of one arm's score by a safety check: per gate id when a gate fired on the run, and the analysis's reasons. */
export interface ReportThrownOut {
	runs: number;
	checks?: Record<string, number>;
	reasons?: string[];
}

export interface ReportCurvePoint {
	k: number;
	tasks: number;
	pass_at_k: number;
	pass_at_k_low: number;
	pass_at_k_high: number;
	pass_hat_k: number;
	pass_hat_k_low: number;
	pass_hat_k_high: number;
}

/** null means unknown, never zero. */
export interface ReportReliabilityCard {
	k: number;
	tasks: number;
	pass1: number | null;
	pass_at_k: number | null;
	pass_hat_k: number | null;
	curve: ReportCurvePoint[] | null;
	consistency: number | null;
	flip_rate: number | null;
	cost_per_solved: number | null;
}

/** One arm under one fault vs its clean runs on the same tasks. null means unknown. */
export interface ReportResilienceArm {
	tasks: number;
	success_clean: number;
	success_fault: number;
	recovery_rate: number | null;
	degradation_ratio: number | null;
	degradation_ratio_low: number | null;
	degradation_ratio_high: number | null;
	cost_inflation: number | null;
	cost_inflation_low: number | null;
	cost_inflation_high: number | null;
	time_to_recover_s: number | null;
	recovered_share: number | null;
	retry_after_respected: number | null;
	/** Paired tasks whose result changed under the fault; with none there is no range. Missing on older reports. */
	changed_tasks?: number;
}

export interface ReportResilienceRow {
	fault: string;
	benchmarks: string[];
	arms: Record<string, ReportResilienceArm>;
}

export interface ReportReliabilityRow {
	fault: string;
	benchmarks?: string[];
	/** drop is the change from clean on the same tasks (negative: the fault hurt); unpaired counts fault runs left out. */
	arms: Record<string, { score: number; drop: number; runs?: number; unpaired?: number }>;
}

export interface ReportSafety {
	attack_resisted: Record<string, number>;
	cheat_rate: Record<string, number>;
}

/** One arm's failure rate in a safety category, with its exact 95% interval. */
export interface ReportAlignmentRate {
	rate: number;
	ci_low: number;
	ci_high: number;
	fired: number;
	runs: number;
}

/** A safety and alignment category. An arm missing from arms was not tested. */
export interface ReportAlignmentCategory {
	id: string;
	label: string;
	description?: string;
	arms: Record<string, ReportAlignmentRate>;
	tests?: string[];
}

/** One OWASP Top 10 for Agentic Applications id and whether this evaluation tested it. */
export interface ReportAgenticCoverage {
	id: string;
	name: string;
	verified?: boolean;
	tested: boolean;
	tests?: string[];
}

export interface ReportAlignment {
	categories: ReportAlignmentCategory[];
	/** Honest abort on impossible tasks, higher is better. */
	impossible_flagged?: Record<string, ReportAlignmentRate>;
	owasp_agentic?: ReportAgenticCoverage[];
}

export interface ReportOverall {
	winner: string;
	wins: Record<string, number>;
	summary: string;
	delta?: number;
	significant?: boolean;
	p_value?: number;
}

/** One point of a learning curve: mean success at one place in the task sequence. */
export interface ReportLearningPoint {
	index: number;
	success: number;
	runs: number;
}

/** Learning over time of one agent on one benchmark run as a task sequence with memory kept. Null: not enough runs to tell. */
export interface ReportLearningRow {
	benchmark: string;
	arm: string;
	/** The no-memory control the gain from memory is measured against, e.g. "codex v2 on gpt-5 (no memory)". */
	control?: string;
	aulc: number | null;
	final_gain: number | null;
	bwt: number | null;
	fwt: number | null;
	episodes_to_threshold: number | null;
	threshold: number;
	curve: ReportLearningPoint[];
	chains: number;
}

export interface ReportRate {
	value: number;
	runs: number;
}

/** Persistence of one agent: resume after a kill, finishing under a cut budget, success per dollar. */
export interface ReportPersistenceRow {
	arm: string;
	resumed_without_redo?: ReportRate;
	graceful_finish?: ReportRate;
	cost_normalized_success?: ReportRate;
}

/** Call quality of one agent on the voice benchmarks; a field no call measured is absent. */
export interface ReportVoiceRow {
	arm: string;
	time_to_first_audio_ms?: ReportRate;
	turn_latency_p50_ms?: ReportRate;
	turn_latency_p95_ms?: ReportRate;
	barge_in_handled?: ReportRate;
	dead_air_s?: ReportRate;
	talk_overs?: ReportRate;
	stt_wer?: ReportRate;
	voice_cost_per_minute?: ReportRate;
}

/** One agent on the voice benchmarks under one simulated caller (a persona or an audio test); absent fields were not measured. */
export interface ReportVoiceCallRow {
	arm: string;
	/** "clean" for the plain caller, else the persona or audio test id. */
	condition: string;
	runs: number;
	successes: number;
	success_rate: number;
	time_to_first_audio_ms?: ReportRate;
	turn_latency_p95_ms?: ReportRate;
	barge_in_handled?: ReportRate;
	audio_jitter_p95_ms?: ReportRate;
	/** Word error rate of the agent's speech recognition against what the caller said (0 to 1, lower is better). */
	stt_wer?: ReportRate;
}

/** GET /evaluations/{id}/report and GET /agents/{id}/report-card */
export interface Report {
	evaluation: string;
	status?: string;
	/** public, or held_out for a held-out evaluation. */
	split?: "public" | "held_out" | string;
	/** The evaluation's mode. "score" shows one arm and never an A/B verdict. */
	mode?: EvaluationMode | string;
	/** How the arms reached the tasks' MCP tool servers; absent is direct. */
	mcp_route?: EvaluationMCPRoute | string;
	arms: ReportArm[];
	capabilities: ReportCapabilityRow[];
	benchmarks: ReportBenchmarkRow[];
	reliability: ReportReliabilityRow[];
	resilience?: ReportResilienceRow[];
	safety?: ReportSafety;
	/** Safety and alignment: failure rate per risk category with intervals, or not tested. */
	alignment?: ReportAlignment;
	/** Only for A/B modes. */
	overall?: ReportOverall;
	/** Runs left out because the scripted practice model answered them. */
	practice_runs?: number;
	/** Why runs were left out, when practice_runs is not zero. */
	note?: string;
	/** Per arm label, over every benchmark. */
	totals?: Record<string, ReportTotals>;
	/** Only when a benchmark ran as a task sequence with memory kept. */
	learning?: ReportLearningRow[];
	/** Only when runs reported resume, budget or success per dollar numbers. */
	persistence?: ReportPersistenceRow[];
	/** Only when some runs were voice calls. */
	voice?: ReportVoiceRow[];
	/** Scores per task segment (tasks grouped by one metadata key). */
	slices?: ReportSlice[];
	/** The metadata filter this report was narrowed to (?where=key:value). */
	where?: Record<string, string>;
	/** Only when voice calls ran with personas or audio tests besides the plain caller. */
	voice_calls?: ReportVoiceCallRow[];
	/** Through Bifrost (C09): per arm, what the Bifrost gateway did across the runs. */
	bifrost?: ReportBifrostRow[];
	/** Only when the evaluation ran the provider failover option. */
	failover?: FailoverReport;
	/** Only when the evaluation ran a red-team benchmark (Z04). */
	red_team?: ReportRedTeam;
	/** Runs thrown out by a safety check over every benchmark (fix wave 3, N215). */
	thrown_out_runs?: number;
	/** Counted runs planned for the shown setups, and those finished (N182). */
	planned_runs?: number;
	done_runs?: number;
}

/** One metadata key and its values, each with per arm scores over the tasks with that value. */
export interface ReportSlice {
	key: string;
	values: ReportSliceValue[];
	/** Per arm label, the value with the lowest score (small values never count). */
	worst: Record<string, string>;
	/** Values not listed. */
	other?: number;
}

export interface ReportSliceValue {
	value: string;
	tasks: number;
	/** Fewer than 3 tasks: shown but never ranked. */
	small?: boolean;
	arms: Record<string, ReportMetrics>;
	/** A/B only: second arm score minus first, as a fraction. */
	delta?: number;
}

/** The report request; where narrows the whole report to tasks with that metadata. */
export type EvaluationReportArg = string | { id: string; where?: Record<string, string> };

/** URL for GET /evaluations/{id}/report with one where=key:value param per filter entry. */
export function evaluationReportUrl(arg: EvaluationReportArg): string {
	const { id, where } = typeof arg === "string" ? { id: arg, where: undefined } : arg;
	const base = `/evaluations/${encodeURIComponent(id)}/report`;
	const qs = Object.entries(where ?? {})
		.map(([k, v]) => `where=${encodeURIComponent(`${k}:${v}`)}`)
		.join("&");
	return withOrgParam(qs ? `${base}?${qs}` : base);
}

/**
 * GET /evaluations/{id}/card (Q-121): the report card tab in one call. Each part is what its own
 * route answers; a part that failed on the server is left out, so read every one as optional.
 */
export interface EvaluationCard {
	evaluation: Evaluation;
	report?: Report;
	failure_patterns?: FailurePatterns;
}

/** URL for GET /evaluations/{id}/card, with the same where filters as the report. */
export function evaluationCardUrl(arg: EvaluationReportArg): string {
	return evaluationReportUrl(arg).replace(/\/report(?=\?|$)/, "/card");
}

/** One arm's Bifrost numbers over every benchmark; an absent count was not observed by any run. */
export interface ReportBifrostRow {
	arm: string;
	runs: number;
	fallback_runs?: number;
	fallback_known: number;
	cache_hits?: number;
	retries?: number;
	input_tokens?: number;
	cost_usd?: number;
	cost_runs: number;
}

/** Red-team section: how often adaptive attacks broke each arm (lower is better). */
export interface ReportRedTeam {
	benchmarks: string[];
	arms: Record<string, ReportRedTeamArm>;
}

/** Attack success of one group (a category, strategy or channel). */
export interface ReportRedTeamRow {
	id: string;
	label: string;
	attacks: number;
	broken: number;
	success_rate: number;
	ci_low: number;
	ci_high: number;
}

/** One successful attack run. */
export interface ReportRedTeamAttack {
	task: string;
	run: string;
	category: string;
	strategy: string;
	channel: string;
	summary: string;
	attempts_to_break: number;
}

/** One arm's red-team numbers. */
export interface ReportRedTeamArm {
	attacks: number;
	broken: number;
	success_rate: number;
	ci_low: number;
	ci_high: number;
	median_attempts_to_break: number | null;
	fewest_attempts_to_break: number | null;
	by_category: ReportRedTeamRow[];
	by_strategy: ReportRedTeamRow[];
	by_channel: ReportRedTeamRow[];
	hardest?: ReportRedTeamAttack;
	most_effective?: ReportRedTeamRow;
	regressions: number;
}

/** One arm's numbers over the whole evaluation; absent fields are unknown. */
export interface ReportTotals {
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
	solved: number;
	/** Clean cost; null when any clean run's cost is not known (never a partial sum). */
	cost_usd: number | null;
	cost_runs?: number;
	cost_per_solved_usd?: number;
	false_completion?: number;
	fault_survival?: number;
	fault_runs?: number;
}

// Names other lanes use for the same shapes.
export type ReportCardResponse = Report;
export type EvaluationReport = Report;
export type ReportArmMetrics = ReportMetrics;
export type ReportCapability = ReportCapabilityRow;
export type ReportBenchmark = ReportBenchmarkRow;
export type ReportReliability = ReportReliabilityRow;
export type AgentCompatibility = Compatibility;
export type BenchmarkCompatibility = CompatibilityBenchmark;
export type EvaluationPack = Pack;
export type EvaluationEstimate = Estimate;
export type EvaluationRange = Range;
export type EstimateRange = Range;
export type AgentVersionsResponse = AgentVersionList;

export interface AgentReportCardParams {
	agent: string;
	version?: number;
	model?: string;
}

/** The agent report card request: version and model only when set, so the server picks the latest. */
export function agentReportCardRequest({ agent, version, model }: AgentReportCardParams): { url: string; params?: Record<string, string> } {
	const params: Record<string, string> = {};
	if (version) params.version = String(version);
	if (model) params.model = model;
	const url = `/agents/${encodeURIComponent(agent)}/report-card`;
	return Object.keys(params).length > 0 ? { url, params } : { url };
}

/** A report card with nothing in it: what an agent with no evaluation gets. */
export function emptyReport(): Report {
	return { evaluation: "", arms: [], capabilities: [], benchmarks: [], reliability: [] };
}

/**
 * GET /agents/{id}/report-card answers {agent, version, model, report, models}, with report null when no
 * evaluation ran the agent. Both that wrapper and a bare report become a Report; no report is an
 * empty one (evaluation ""). models (the models the agent's evaluations used) is kept on the result.
 */
export function unwrapAgentReportCard(body: unknown): Report & { models?: string[] } {
	if (body && typeof body === "object" && "report" in body) {
		const { report, models } = body as { report?: Report | null; models?: string[] };
		const out: Report & { models?: string[] } = { ...(report ?? emptyReport()) };
		if (Array.isArray(models)) out.models = models;
		return out;
	}
	return (body as Report | null) ?? emptyReport();
}

/** The list answers either a bare array or {evaluations: [...]}; both become an array. */
export function normalizeEvaluationList(raw: unknown): Evaluation[] {
	if (Array.isArray(raw)) return raw as Evaluation[];
	if (raw && typeof raw === "object" && Array.isArray((raw as { evaluations?: unknown }).evaluations)) {
		return (raw as { evaluations: Evaluation[] }).evaluations;
	}
	return [];
}

/** listEvaluations' optional parameters (G3). status is a comma list on the server. */
export interface ListEvaluationsParams {
	limit?: number;
	offset?: number;
	q?: string;
	status?: string | readonly string[];
	fields?: "light";
	/** Phase D decision 6: "include" also lists archived evaluations, "only" lists just them. */
	archived?: "include" | "only";
}

/** The search parameters of an evaluations list call: only set ones. Pure. */
export function listEvaluationsSearch(params: ListEvaluationsParams | void): Record<string, string | number> {
	const out: Record<string, string | number> = {};
	if (!params) return out;
	if (typeof params.limit === "number") out.limit = params.limit;
	if (typeof params.offset === "number") out.offset = params.offset;
	if (params.q && params.q.trim()) out.q = params.q.trim();
	const status = typeof params.status === "string" ? params.status : params.status?.join(",");
	if (status) out.status = status;
	if (params.fields) out.fields = params.fields;
	if (params.archived) out.archived = params.archived;
	return out;
}

// The agent leaderboard (A17) lives in ./agentLeaderboardApi; its types are re-exported here.
export type { AgentCapabilityScore as AgentLeaderboardCell, AgentLeaderboardRow, AgentLeaderboardResponse } from "./agentLeaderboardApi";
export { useGetAgentLeaderboardQuery } from "./agentLeaderboardApi";

/** Built-in library agents marked baseline on the agent leaderboard. */
export const BASELINE_AGENTS = ["codex", "claude-code", "mini-swe-agent", "opencode", "gemini-cli"];

// ---------------------------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------------------------

const enc = encodeURIComponent;

export const evaluationsApi = benchApi
	.enhanceEndpoints({
		addTagTypes: ["Evaluations", "AgentVersions", "AgentCompat", "AgentLeaderboard"],
	})
	.injectEndpoints({
		endpoints: (builder) => ({
			getAgentCompatibility: builder.query<Compatibility, string>({
				query: (agent) => `/agents/${enc(agent)}/compatibility`,
				providesTags: (_r, _e, agent) => [{ type: "AgentCompat", id: agent }],
			}),
			getAgentVersions: builder.query<AgentVersionList, string>({
				query: (agent) => `/agents/${enc(agent)}/versions`,
				providesTags: (_r, _e, agent) => [{ type: "AgentVersions", id: agent }],
			}),
			getAgentVersion: builder.query<AgentVersionDetail, { agent: string; version: number }>({
				query: ({ agent, version }) => `/agents/${enc(agent)}/versions/${version}`,
				providesTags: (_r, _e, { agent, version }) => [{ type: "AgentVersions", id: `${agent}@${version}` }],
			}),
			snapshotAgentVersion: builder.mutation<AgentVersionCreated, { agent: string; note?: string }>({
				query: ({ agent, note }) => ({
					url: `/agents/${enc(agent)}/versions`,
					method: "POST",
					body: { note: note ?? "" },
				}),
				invalidatesTags: (_r, _e, { agent }) => [{ type: "AgentVersions", id: agent }],
			}),
			// A plain call asks the whole list; limit and offset page it on the server (G3).
			listEvaluations: builder.query<Evaluation[], ListEvaluationsParams | void>({
				query: (params) => {
					const search = listEvaluationsSearch(params);
					return Object.keys(search).length > 0 ? { url: "/evaluations", params: search } : "/evaluations";
				},
				transformResponse: (body: unknown) => normalizeEvaluationList(body),
				providesTags: [{ type: "Evaluations", id: "LIST" }],
			}),
			// The light rows of finished evaluations (M7-41): fields=light, status=done by default. Same LIST tag.
			listEvaluationsLight: builder.query<Evaluation[], ListEvaluationsParams | void>({
				query: (params) => ({ url: "/evaluations", params: listEvaluationsSearch({ status: "done", ...(params ?? {}), fields: "light" }) }),
				transformResponse: (body: unknown) => normalizeEvaluationList(body),
				providesTags: [{ type: "Evaluations", id: "LIST" }],
			}),
			getEvaluation: builder.query<Evaluation, string>({
				query: (id) => `/evaluations/${enc(id)}`,
				providesTags: (_r, _e, id) => [{ type: "Evaluations", id }],
			}),
			createEvaluation: builder.mutation<Evaluation, EvaluationSpec>({
				query: (body) => ({ url: "/evaluations", method: "POST", body }),
				invalidatesTags: [
					{ type: "Evaluations", id: "LIST" },
					{ type: "BenchExperiment", id: "LIST" },
				],
			}),
			cancelEvaluation: builder.mutation<Evaluation, string>({
				query: (id) => ({
					url: `/evaluations/${enc(id)}/cancel`,
					method: "POST",
				}),
				invalidatesTags: (_r, _e, id) => [
					{ type: "Evaluations", id },
					{ type: "Evaluations", id: "LIST" },
				],
			}),
			/** Raise the budget and continue the benchmarks the spend cap stopped. */
			raiseEvaluationBudget: builder.mutation<Evaluation, RaiseBudgetRequest>({
				query: ({ id, budget_usd }) => ({ url: `/evaluations/${enc(id)}/raise-budget`, method: "POST", body: { budget_usd } }),
				invalidatesTags: (_r, _e, { id }) => [
					{ type: "Evaluations", id },
					{ type: "Evaluations", id: "LIST" },
				],
			}),
			estimateEvaluation: builder.mutation<Estimate, EvaluationSpec>({
				query: (body) => ({
					url: "/evaluations/estimate",
					method: "POST",
					body,
				}),
			}),
			/** The same estimate as a query, so a form can show it live (cached per spec). */
			getEvaluationEstimate: builder.query<Estimate, EstimateRequest>({
				query: (body) => ({
					url: "/evaluations/estimate",
					method: "POST",
					body,
				}),
			}),
			getEvaluationPacks: builder.query<Pack[], void>({
				query: () => "/evaluation-packs",
				transformResponse: (body: Pack[] | { packs?: Pack[] | null } | null) => (Array.isArray(body) ? body : (body?.packs ?? [])),
			}),
			/** The fault catalog for the wizard (clean excluded). */
			getBenchFaults: builder.query<FaultCatalogItem[], void>({
				query: () => "/faults",
				transformResponse: (body: { faults?: FaultCatalogItem[] | null } | FaultCatalogItem[] | null) =>
					Array.isArray(body) ? body : (body?.faults ?? []),
			}),
			getEvaluationReport: builder.query<Report, EvaluationReportArg>({
				query: (arg) => evaluationReportUrl(arg),
				providesTags: (_r, _e, arg) => [{ type: "Evaluations", id: `${typeof arg === "string" ? arg : arg.id}/report` }],
			}),
			getEvaluationCard: builder.query<EvaluationCard, EvaluationReportArg>({
				query: (arg) => evaluationCardUrl(arg),
				providesTags: (_r, _e, arg) => {
					const id = typeof arg === "string" ? arg : arg.id;
					return [
						{ type: "Evaluations", id },
						{ type: "Evaluations", id: `${id}/report` },
					];
				},
			}),
			getAgentReportCard: builder.query<Report & { models?: string[] }, { agent: string; version?: number; model?: string }>({
				query: (params) => agentReportCardRequest(params),
				transformResponse: (body: unknown) => unwrapAgentReportCard(body),
				providesTags: (_r, _e, { agent }) => [{ type: "Evaluations", id: `card/${agent}` }],
			}),
		}),
	});

export const {
	useGetAgentCompatibilityQuery,
	useListEvaluationsLightQuery,
	useGetAgentVersionsQuery,
	useGetAgentVersionQuery,
	useSnapshotAgentVersionMutation,
	useListEvaluationsQuery,
	useGetEvaluationQuery,
	useGetEvaluationCardQuery,
	useCreateEvaluationMutation,
	useCancelEvaluationMutation,
	useRaiseEvaluationBudgetMutation,
	useEstimateEvaluationMutation,
	useGetEvaluationPacksQuery,
	useGetEvaluationReportQuery,
	useGetAgentReportCardQuery,
	useGetEvaluationEstimateQuery,
	useGetBenchFaultsQuery,
} = evaluationsApi;

// Hook names other lanes use.
export const useGetEvaluationsQuery = useListEvaluationsQuery;
export const useCreateAgentVersionMutation = useSnapshotAgentVersionMutation;
export const useEstimateEvaluationQuery = useGetEvaluationEstimateQuery;