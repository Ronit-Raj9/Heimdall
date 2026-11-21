import { benchApi } from "./benchApi";

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
	note: string;
	/** Runs made with this version; present on the list endpoint. */
	runs?: number;
	/** The Files API version of agents/<id>.yaml this snapshot was taken from (GET /project/versions/agents/<id>.yaml). */
	file_version?: string;
	file_sha256?: string;
}

/** GET /agents/{id}/versions */
export interface AgentVersionList {
	agent: string;
	versions: AgentVersion[];
	/** The version holding the saved config: what pull requests are compared with. */
	live?: number;
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
	/** Default 5, admitted tasks only. */
	tasks_per_benchmark: number;
	/** Repeats per task, default 2. */
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
}

/** What an evaluation must show to pass. Scores are fractions (0 to 1). */
export interface EvaluationGate {
	/** The version compared with; 0 or absent: the agent's live version (the last one released). */
	baseline_version?: number;
	/** Lowest score per capability ("all" is every benchmark pooled). */
	min_score?: Record<string, number>;
	/** Largest score drop from the baseline on any capability (0.05 is 5 points). */
	max_regression?: number;
}

export type GateState = "pass" | "fail" | "no_baseline";

export interface GateCheck {
	capability: string;
	rule: "min_score" | "max_regression" | string;
	threshold: number;
	observed: number;
	baseline?: number;
	pass: boolean;
	summary: string;
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
	/** Tasks left out of this benchmark alone (quarantined); the rest runs. */
	skipped_tasks?: EvaluationSkippedTask[];
	/** Condition id to fault (or "attack"), fixed at launch. */
	fault_of?: Record<string, string>;
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
}

export interface ReportResilienceRow {
	fault: string;
	benchmarks: string[];
	arms: Record<string, ReportResilienceArm>;
}

export interface ReportReliabilityRow {
	fault: string;
	benchmarks?: string[];
	arms: Record<string, { score: number; drop: number; runs?: number }>;
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
export interface ReportCurvePoint {
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
	curve: ReportCurvePoint[];
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

/** GET /evaluations/{id}/report and GET /agents/{id}/report-card */
export interface Report {
	evaluation: string;
	status?: string;
	/** public, or held_out for a held-out evaluation. */
	split?: "public" | "held_out" | string;
	/** The evaluation's mode. "score" shows one arm and never an A/B verdict. */
	mode?: EvaluationMode | string;
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
	/** Only when a benchmark ran as a task sequence with memory kept. */
	learning?: ReportLearningRow[];
	/** Only when runs reported resume, budget or success per dollar numbers. */
	persistence?: ReportPersistenceRow[];
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
 * GET /agents/{id}/report-card answers {agent, version, model, report}, with report null when no
 * evaluation ran the agent. Both that wrapper and a bare report become a Report; no report is an
 * empty one (evaluation "").
 */
export function unwrapAgentReportCard(body: unknown): Report {
	if (body && typeof body === "object" && "report" in body) {
		const report = (body as { report?: Report | null }).report;
		return report ?? emptyReport();
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
			snapshotAgentVersion: builder.mutation<AgentVersion, { agent: string; note?: string }>({
				query: ({ agent, note }) => ({
					url: `/agents/${enc(agent)}/versions`,
					method: "POST",
					body: { note: note ?? "" },
				}),
				invalidatesTags: (_r, _e, { agent }) => [{ type: "AgentVersions", id: agent }],
			}),
			listEvaluations: builder.query<Evaluation[], void>({
				query: () => "/evaluations",
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
			estimateEvaluation: builder.mutation<Estimate, EvaluationSpec>({
				query: (body) => ({
					url: "/evaluations/estimate",
					method: "POST",
					body,
				}),
			}),
			/** The same estimate as a query, so a form can show it live (cached per spec). */
			getEvaluationEstimate: builder.query<Estimate, EvaluationSpec>({
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
			getEvaluationReport: builder.query<Report, string>({
				query: (id) => `/evaluations/${enc(id)}/report`,
				providesTags: (_r, _e, id) => [{ type: "Evaluations", id: `${id}/report` }],
			}),
			getAgentReportCard: builder.query<Report, { agent: string; version?: number; model?: string }>({
				query: (params) => agentReportCardRequest(params),
				transformResponse: (body: unknown) => unwrapAgentReportCard(body),
				providesTags: (_r, _e, { agent }) => [{ type: "Evaluations", id: `card/${agent}` }],
			}),
		}),
	});

export const {
	useGetAgentCompatibilityQuery,
	useGetAgentVersionsQuery,
	useGetAgentVersionQuery,
	useSnapshotAgentVersionMutation,
	useListEvaluationsQuery,
	useGetEvaluationQuery,
	useCreateEvaluationMutation,
	useCancelEvaluationMutation,
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