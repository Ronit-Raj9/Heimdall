/**
 * Typed client for the public Heimdall API v1 (/api/v1, docs/api-v1.md).
 *
 * V1Client covers the stable routes: agents and their versions, compatibility,
 * benchmarks, evaluations, estimates, report cards, the agent leaderboard,
 * runs and trace export. evaluateAndWait starts an evaluation, waits for it
 * and resolves to the final evaluation with its report card.
 */
import { Client, HeimdallError, HeimdallTimeout, RawResponse, type ClientOptions } from "./client.ts";
import type { Mode, RunList, Triage } from "./types.ts";

export const API_V1_PREFIX = "/api/v1";
export const EVALUATION_TERMINAL_STATES: readonly string[] = ["done", "failed", "cancelled"];

/** Every v1 route the client calls, as [method, OpenAPI path]; the live test checks them against GET /api/v1/openapi.json. */
export const V1_ROUTES: readonly (readonly [string, string])[] = [
	["GET", "/api/v1/agents"],
	["GET", "/api/v1/agents/{id}/versions"],
	["POST", "/api/v1/agents/{id}/versions"],
	["GET", "/api/v1/agents/{id}/versions/{n}"],
	["GET", "/api/v1/agents/{id}/compatibility"],
	["GET", "/api/v1/agents/{id}/report-card"],
	["GET", "/api/v1/benchmarks"],
	["GET", "/api/v1/benchmarks/catalog"],
	["GET", "/api/v1/benchmarks/packs"],
	["GET", "/api/v1/benchmarks/{id}/tasks/{task}"],
	["GET", "/api/v1/evaluations"],
	["POST", "/api/v1/evaluations"],
	["POST", "/api/v1/evaluations/estimate"],
	["GET", "/api/v1/evaluations/{id}"],
	["POST", "/api/v1/evaluations/{id}/cancel"],
	["GET", "/api/v1/evaluations/{id}/report"],
	["POST", "/api/v1/estimate"],
	["GET", "/api/v1/experiments/{id}/triage"],
	["GET", "/api/v1/leaderboard/agents"],
	["GET", "/api/v1/runs"],
	["GET", "/api/v1/runs/{id}"],
	["GET", "/api/v1/runs/{id}/steps"],
	["GET", "/api/v1/runs/{id}/trace"],
	["GET", "/api/v1/runs/{id}/otlp"],
];

/** Every JSON answer carries the server mode. */
export interface Moded {
	mode: Mode;
}

export interface EvaluationArm {
	label: string;
	agent: string;
	version?: number;
	model: string;
	provider?: string;
}

/** Body of POST /api/v1/evaluations: one agent (or two arms for an A/B) across benchmarks. */
export interface EvaluationSpec {
	name: string;
	mode: "score" | "versions" | "models" | "opponent";
	arms: EvaluationArm[];
	benchmarks?: string[];
	pack?: string;
	tasks_per_benchmark?: number;
	trials?: number;
	faults?: string[];
	budget_usd?: number;
}

export type EvaluationStatus = "queued" | "running" | "done" | "failed" | "cancelled";

export interface EvaluationChild {
	benchmark: string;
	experiment_id: string;
	status: string;
	skipped?: string;
}

export interface Evaluation extends Moded {
	id: string;
	org?: string;
	spec: EvaluationSpec;
	status: EvaluationStatus;
	children: EvaluationChild[];
	created_at: string;
	created_by?: string;
}

export interface EvaluationList extends Moded {
	evaluations: Evaluation[];
}

export interface Range {
	low: number;
	high: number;
	known: boolean;
}

export interface EvaluationEstimate extends Moded {
	runs: number;
	est_cost_usd: Range;
	est_minutes: Range;
	per_benchmark: Record<string, unknown>[];
	notes: string[];
}

export interface AgentVersionEntry {
	version: number;
	hash: string;
	created_at: string;
	created_by: string;
	note: string;
	runs: number;
}

export interface AgentVersionList extends Moded {
	agent: string;
	builtin?: boolean;
	versions: AgentVersionEntry[];
}

export interface AgentVersionDoc extends Moded {
	version: number;
	hash: string;
	yaml: string;
}

export interface BenchmarkCompatibility {
	id: string;
	ok: boolean;
	requires: string[];
	missing: string[];
	reason: string;
}

export interface AgentCompatibility extends Moded {
	agent: string;
	capabilities: string[];
	defaulted: boolean;
	benchmarks: BenchmarkCompatibility[];
}

export interface LeaderboardCapability {
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
	benchmarks: string[];
}

export interface LeaderboardRow {
	agent: string;
	version: number;
	model: string;
	label: string;
	config_hash: string;
	baseline: boolean;
	capabilities: Record<string, LeaderboardCapability>;
	cost_usd: number | null;
	runs: number;
	last_evaluation: string;
	last_experiment: string;
}

export interface AgentLeaderboard extends Moded {
	capabilities: string[];
	models: string[];
	capability: string;
	model: string;
	rows: LeaderboardRow[];
	generated_at: string;
}

// ---- Report card ----

export interface ReportArm {
	label: string;
	agent: string;
	version?: number;
	model: string;
}

/** One arm's numbers: verified success rate with its 95% interval, and means per run. */
export interface Metrics {
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
	cost_usd: number;
	tokens: number;
	steps: number;
	tool_calls: number;
	wall_s: number;
}

export interface BenchmarkVerdict {
	benchmark?: string;
	winner: string;
	delta: number;
	significant: boolean;
	summary: string;
	tasks: number;
	p_value?: number;
	p_value_holm?: number;
	ci_low?: number;
	ci_high?: number;
}

export interface OverallVerdict {
	winner: string;
	wins: Record<string, number>;
	delta: number;
	significant: boolean;
	p_value?: number;
	tasks: number;
	benchmarks: number;
	summary: string;
}

export interface CapabilityScore {
	capability: string;
	benchmarks: string[];
	arms: Record<string, Metrics>;
}

export interface BenchmarkScore {
	benchmark: string;
	capability: string;
	experiment_id: string;
	arms: Record<string, Metrics>;
	verdict?: BenchmarkVerdict;
}

export interface Reliability {
	fault: string;
	benchmarks: string[];
	arms: Record<string, { score: number; drop: number; runs: number }>;
}

export interface Safety {
	attack_resisted: Record<string, number>;
	cheat_rate: Record<string, number>;
}

/** The report card of one evaluation (GET /api/v1/evaluations/{id}/report). */
export interface ReportCard {
	mode: string;
	evaluation: string;
	status?: string;
	arms: ReportArm[];
	capabilities: CapabilityScore[];
	benchmarks: BenchmarkScore[];
	reliability: Reliability[];
	safety: Safety;
	overall?: OverallVerdict;
}

export interface AgentReportCard extends Moded {
	agent: string;
	version?: number;
	model?: string;
	report: ReportCard | null;
}

/** Fills the lists and maps a report card may leave out, so callers can index them without checks. */
export function normalizeReportCard(raw: Partial<ReportCard> & Record<string, unknown>): ReportCard {
	const metrics = (m: unknown): Metrics => {
		const o = (m ?? {}) as Partial<Metrics>;
		return {
			score: o.score ?? 0, ci_low: o.ci_low ?? 0, ci_high: o.ci_high ?? 0, runs: o.runs ?? 0, cost_usd: o.cost_usd ?? 0,
			tokens: o.tokens ?? 0, steps: o.steps ?? 0, tool_calls: o.tool_calls ?? 0, wall_s: o.wall_s ?? 0,
		};
	};
	const arms = (a: unknown): Record<string, Metrics> =>
		Object.fromEntries(Object.entries((a ?? {}) as Record<string, unknown>).map(([k, v]) => [k, metrics(v)]));
	return {
		mode: String(raw.mode ?? ""),
		evaluation: String(raw.evaluation ?? ""),
		...(raw.status ? { status: raw.status } : {}),
		arms: raw.arms ?? [],
		capabilities: (raw.capabilities ?? []).map((c) => ({ ...c, benchmarks: c.benchmarks ?? [], arms: arms(c.arms) })),
		benchmarks: (raw.benchmarks ?? []).map((b) => ({ ...b, arms: arms(b.arms) })),
		reliability: (raw.reliability ?? []).map((r) => ({ ...r, benchmarks: r.benchmarks ?? [], arms: r.arms ?? {} })),
		safety: { attack_resisted: raw.safety?.attack_resisted ?? {}, cheat_rate: raw.safety?.cheat_rate ?? {} },
		...(raw.overall ? { overall: raw.overall } : {}),
	};
}

/** The arm's verified success rate over every benchmark of the report, weighted by runs; null without runs. */
export function armScore(report: ReportCard, arm: string): number | null {
	let runs = 0;
	let total = 0;
	for (const b of report.benchmarks) {
		const m = b.arms[arm];
		if (m && m.runs > 0) {
			runs += m.runs;
			total += m.score * m.runs;
		}
	}
	return runs > 0 ? total / runs : null;
}

/** What evaluateAndWait resolves to. */
export interface EvaluationResult {
	evaluation: Evaluation;
	report: ReportCard;
	/** The evaluation finished with status done. */
	succeeded: boolean;
}

type Query = Record<string, string | number | boolean | undefined | null>;

const seg = (value: string) => encodeURIComponent(value);
const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const clean = (q: Query): Record<string, string | number | undefined | null> =>
	Object.fromEntries(Object.entries(q).map(([k, v]) => [k, typeof v === "boolean" ? (v ? "true" : undefined) : v]));

class V1Transport extends Client {
	protected override readonly apiPrefix: string = API_V1_PREFIX;
	getJSON<T>(path: string, query?: Query, deadline?: number): Promise<T> { return this.get<T>(path, query ? clean(query) : undefined, deadline); }
	postJSON<T>(path: string, payload: unknown): Promise<T> { return this.post<T>(path, payload); }
	getRaw(path: string, query?: Query): Promise<RawResponse> { return this.raw(path, query ? clean(query) : undefined); }
}

/**
 * Client for the public Heimdall API v1. token is an organization API token
 * (hosted; its scope must cover the calls: read, evaluate or admin) or the
 * local write token.
 */
export class V1Client {
	private readonly t: V1Transport;

	constructor(baseUrl: string, options: ClientOptions = {}) {
		this.t = new V1Transport(baseUrl, options);
	}

	get baseUrl(): string { return this.t.baseUrl; }
	get lastMode(): string | undefined { return this.t.lastMode; }

	/** The OpenAPI 3.1 document of API v1. */
	openapi(): Promise<Record<string, unknown> & { openapi: string; paths: Record<string, Record<string, unknown>> }> { return this.t.getJSON("/openapi.json"); }

	// Agents
	agents(): Promise<Moded & Record<string, unknown>> { return this.t.getJSON("/agents"); }
	agentVersions(agent: string): Promise<AgentVersionList> { return this.t.getJSON(`/agents/${seg(agent)}/versions`); }
	agentVersion(agent: string, version: number): Promise<AgentVersionDoc> { return this.t.getJSON(`/agents/${seg(agent)}/versions/${Math.trunc(version)}`); }
	/** Register the agent's current config as a new version (created false when unchanged). */
	createAgentVersion(agent: string, note = ""): Promise<Moded & AgentVersionEntry & { created: boolean }> { return this.t.postJSON(`/agents/${seg(agent)}/versions`, { note }); }
	/** Which benchmarks the agent can run, and what it lacks for the rest. */
	compatibility(agent: string): Promise<AgentCompatibility> { return this.t.getJSON(`/agents/${seg(agent)}/compatibility`); }
	async agentReportCard(agent: string, options: { version?: number; model?: string } = {}): Promise<AgentReportCard> {
		const card = await this.t.getJSON<AgentReportCard>(`/agents/${seg(agent)}/report-card`, { version: options.version, model: options.model });
		return { ...card, report: card.report ? normalizeReportCard({ ...card.report, mode: card.mode }) : null };
	}

	// Benchmarks
	benchmarks(): Promise<Moded & Record<string, unknown>> { return this.t.getJSON("/benchmarks"); }
	benchmarkCatalog(): Promise<Moded & Record<string, unknown>> { return this.t.getJSON("/benchmarks/catalog"); }
	benchmarkPacks(): Promise<Moded & Record<string, unknown>> { return this.t.getJSON("/benchmarks/packs"); }
	benchmarkTask(benchmark: string, task: string): Promise<Moded & Record<string, unknown>> { return this.t.getJSON(`/benchmarks/${seg(benchmark)}/tasks/${seg(task)}`); }

	// Evaluations
	evaluations(): Promise<EvaluationList> { return this.t.getJSON("/evaluations"); }
	evaluation(id: string): Promise<Evaluation> { return this.t.getJSON(`/evaluations/${seg(id)}`); }
	/** Start an evaluation (202): one experiment per benchmark. */
	createEvaluation(spec: EvaluationSpec): Promise<Evaluation> { return this.t.postJSON("/evaluations", spec); }
	cancelEvaluation(id: string): Promise<Evaluation> { return this.t.postJSON(`/evaluations/${seg(id)}/cancel`, undefined); }
	/** Runs, cost and minutes before launching; unknown prices stay unknown (known false). */
	estimateEvaluation(spec: EvaluationSpec): Promise<EvaluationEstimate> { return this.t.postJSON("/evaluations/estimate", spec); }
	/** Forecast one experiment's cost and time from sealed history (YAML text or an object). */
	estimate(experiment: Record<string, unknown> | string): Promise<Moded & Record<string, unknown>> { return this.t.postJSON("/estimate", experiment); }
	async report(id: string): Promise<ReportCard> {
		return normalizeReportCard(await this.t.getJSON<ReportCard & Record<string, unknown>>(`/evaluations/${seg(id)}/report`));
	}
	/** Failure groups of one benchmark's experiment, per arm, with the runs showing each. */
	triage(experimentId: string): Promise<Moded & Triage> { return this.t.getJSON(`/experiments/${seg(experimentId)}/triage`); }

	// Leaderboard and runs
	leaderboard(options: { capability?: string; model?: string } = {}): Promise<AgentLeaderboard> { return this.t.getJSON("/leaderboard/agents", options); }
	/** Runs newest first; filters: exp, suite, arm, task, condition, outcome, track, limit, offset. */
	runs(filters: Query = {}): Promise<Moded & RunList> { return this.t.getJSON("/runs", filters); }
	run(runId: string): Promise<Moded & Record<string, unknown>> { return this.t.getJSON(`/runs/${seg(runId)}`); }
	runSteps(runId: string): Promise<Moded & Record<string, unknown>> { return this.t.getJSON(`/runs/${seg(runId)}/steps`); }
	/** Export a run's trace: json (steps and events), jsonl (steps) or otlp. */
	trace(runId: string, format: "json" | "jsonl" | "otlp" = "json"): Promise<RawResponse> { return this.t.getRaw(`/runs/${seg(runId)}/trace`, { format }); }
	/** A sealed run as an OpenTelemetry trace; payloads adds redacted, capped tool payloads. */
	otlp(runId: string, payloads = false): Promise<RawResponse> { return this.t.getRaw(`/runs/${seg(runId)}/otlp`, { payloads }); }

	// Helpers

	/** Poll until the evaluation is done, failed or cancelled; rejects with HeimdallTimeout after timeoutMs. */
	async waitForEvaluation(
		id: string,
		options: { timeoutMs?: number; intervalMs?: number; onStatus?: (evaluation: Evaluation) => void } = {},
	): Promise<Evaluation> {
		const timeoutMs = options.timeoutMs ?? 6 * 3_600_000;
		const intervalMs = options.intervalMs ?? 10_000;
		const deadline = Date.now() + timeoutMs;
		let status = "unknown";
		for (;;) {
			if (Date.now() >= deadline) {
				throw new HeimdallTimeout(`evaluation ${id} still ${status} after ${Math.round(timeoutMs / 1000)}s`, this.lastMode ?? "");
			}
			const ev = await this.t.getJSON<Evaluation>(`/evaluations/${seg(id)}`, undefined, deadline);
			status = ev.status;
			options.onStatus?.(ev);
			if (EVALUATION_TERMINAL_STATES.includes(ev.status)) return ev;
			const left = deadline - Date.now();
			if (left > 0) await sleep(Math.min(intervalMs, left));
		}
	}

	/**
	 * Start an evaluation, wait for it to finish and resolve to it with its report card. On timeout
	 * the evaluation is cancelled (cancelOnTimeout, default true) and HeimdallTimeout rejects. A
	 * failed or cancelled evaluation still resolves: check succeeded.
	 */
	async evaluateAndWait(
		spec: EvaluationSpec,
		options: { timeoutMs?: number; intervalMs?: number; onStatus?: (evaluation: Evaluation) => void; cancelOnTimeout?: boolean } = {},
	): Promise<EvaluationResult> {
		const created = await this.createEvaluation(spec);
		if (!created.id) throw new HeimdallError(0, "server returned no evaluation id", "decode", this.lastMode ?? "", "POST", "/evaluations");
		let evaluation: Evaluation;
		try {
			evaluation = await this.waitForEvaluation(created.id, options);
		} catch (err) {
			if (err instanceof HeimdallTimeout && options.cancelOnTimeout !== false) {
				await this.cancelEvaluation(created.id).catch(() => undefined);
			}
			throw err;
		}
		return { evaluation, report: await this.report(created.id), succeeded: evaluation.status === "done" };
	}
}
