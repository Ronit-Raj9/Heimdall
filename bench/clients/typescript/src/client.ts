/**
 * Thin client over the Heimdall bench HTTP API (/api/bench). Zero runtime
 * dependencies: it uses the global fetch of Node 18+ and browsers.
 *
 * Every JSON method resolves to the server's object, which always carries the
 * server `mode` (live, recorded or synthetic); raw downloads resolve to a
 * RawResponse whose mode comes from the X-Bench-Mode header. With
 * `requireLive: true` every call rejects with ModeError unless the server says
 * live, so a CI job never mistakes a recorded demo for a real result.
 */
import type {
	AgentsResponse,
	AuditRequest,
	AuditResponse,
	CatalogResponse,
	DiagnosticsResponse,
	DoctorResponse,
	EventsPollResponse,
	Experiment,
	ExperimentCreatedResponse,
	ExperimentListResponse,
	ExperimentResponse,
	GridCell,
	GridResponse,
	HealthResponse,
	ImportsResponse,
	LeaderboardResponse,
	Mode,
	PairResponse,
	PlanResponse,
	PolicyDecision,
	PolicyResult,
	ReleaseListResponse,
	ReleaseRequest,
	ReleaseResponse,
	RunDiffResponse,
	RunEventsResponse,
	RunReceiptsResponse,
	RunResponse,
	SuiteTaskResponse,
	SuitesResponse,
	TriageResponse,
	VerdictResponse,
	WorkersResponse,
} from "./types.ts";

export const API_PREFIX = "/api/bench";
export const MODE_HEADER = "X-Bench-Mode";
export const TERMINAL_STATES: readonly string[] = ["done", "failed", "cancelled"];

/** Every route the client calls, as [method, OpenAPI path]; the contract smoke test checks them against GET /openapi.json. */
export const ROUTES: readonly (readonly [string, string])[] = [
	["GET", "/api/bench/health"],
	["GET", "/api/bench/doctor"],
	["GET", "/api/bench/catalog"],
	["GET", "/api/bench/openapi.json"],
	["GET", "/api/bench/suites"],
	["GET", "/api/bench/suites/{id}/tasks/{task}"],
	["GET", "/api/bench/agents"],
	["GET", "/api/bench/experiments"],
	["POST", "/api/bench/experiments"],
	["POST", "/api/bench/experiments/plan"],
	["POST", "/api/bench/experiments/{id}/start"],
	["GET", "/api/bench/experiments/{id}"],
	["GET", "/api/bench/experiments/{id}/events"],
	["GET", "/api/bench/experiments/{id}/grid"],
	["GET", "/api/bench/experiments/{id}/verdict"],
	["GET", "/api/bench/experiments/{id}/diagnostics"],
	["GET", "/api/bench/experiments/{id}/triage"],
	["GET", "/api/bench/runs/{id}"],
	["GET", "/api/bench/runs/{id}/events"],
	["GET", "/api/bench/runs/{id}/diff"],
	["GET", "/api/bench/runs/{id}/receipts"],
	["GET", "/api/bench/runs/{id}/files/{name}"],
	["GET", "/api/bench/pairs/{id}"],
	["GET", "/api/bench/leaderboard"],
	["POST", "/api/bench/audit"],
	["GET", "/api/bench/releases"],
	["POST", "/api/bench/releases"],
	["GET", "/api/bench/releases/{id}"],
	["GET", "/api/bench/releases/{id}/evidence"],
	["GET", "/api/bench/imports"],
	["GET", "/api/bench/workers"],
];

/** An experiment as an object (sent as JSON) or as YAML text (sent as application/yaml). */
export type ExperimentInput = Experiment | Record<string, unknown> | string;

/** The OpenAPI document; the server mode is under x-bench-mode. */
export type OpenAPIDocument = { openapi: string; paths: Record<string, Record<string, unknown>>; "x-bench-mode": Mode; "x-bench-undocumented": string[] } & Record<string, unknown>;

export interface ClientOptions {
	/** Sent as "Authorization: Bearer <token>". The server requires its write token (BENCH_WRITE_TOKEN) on every POST route; reads need none. */
	token?: string;
	/** Per-request timeout in milliseconds. Default 30000. */
	timeoutMs?: number;
	/** Reject every answer whose mode is not live. */
	requireLive?: boolean;
	/** Retries for reads on network errors and 502/504. Writes never retry. Default 2. */
	readRetries?: number;
	/** fetch implementation; default the global fetch. */
	fetch?: typeof fetch;
}

/** A request the server refused or could not answer. status is 0 when no answer arrived. */
export class HeimdallError extends Error {
	readonly status: number;
	readonly type: string;
	readonly serverMessage: string;
	readonly mode: Mode | "";
	readonly method: string;
	readonly path: string;

	constructor(status: number, message: string, type = "", mode: string = "", method = "", path = "") {
		super(`${method ? `${method} ${path}: ` : ""}${status ? `${status} ${type}: ` : ""}${message}`);
		this.name = "HeimdallError";
		this.status = status;
		this.type = type;
		this.serverMessage = message;
		this.mode = mode as Mode | "";
		this.method = method;
		this.path = path;
	}
}

/** A request or a wait ran out of time. */
export class HeimdallTimeout extends HeimdallError {
	constructor(message: string, mode = "", method = "", path = "") {
		super(0, message, "timeout", mode, method, path);
		this.name = "HeimdallTimeout";
	}
}

/** The server answered in a mode the client was told not to accept. */
export class ModeError extends HeimdallError {
	constructor(mode: string, method: string, path: string) {
		super(0, `server mode is "${mode}", not live: refusing to treat it as a real result`, "mode", mode, method, path);
		this.name = "ModeError";
	}
}

/** A non-JSON answer: a patch, a run file or an evidence zip. */
export class RawResponse {
	readonly data: Uint8Array;
	readonly contentType: string;
	readonly mode: Mode | "";

	constructor(data: Uint8Array, contentType: string, mode: string) {
		this.data = data;
		this.contentType = contentType;
		this.mode = mode as Mode | "";
	}

	text(): string {
		return new TextDecoder().decode(this.data);
	}
}

/** The release gate's answer. Destructures as `const [decision, action, policyResult] = result`. */
export class CheckResult implements Iterable<unknown> {
	readonly decision: string;
	readonly action: string;
	readonly policyResult: PolicyResult | null;
	readonly mode: Mode;
	readonly verdict: VerdictResponse;

	constructor(verdict: VerdictResponse) {
		this.verdict = verdict;
		this.decision = verdict.verdict.decision;
		this.action = verdict.verdict.action;
		this.policyResult = verdict.policy ?? null;
		this.mode = verdict.mode;
	}

	/** allow, warn or block; null when the experiment has no policy. */
	get policyDecision(): PolicyDecision | null {
		return this.policyResult ? this.policyResult.decision : null;
	}

	/** The release policy says block. */
	get blocked(): boolean {
		return this.policyDecision === "block";
	}

	get live(): boolean {
		return this.mode === "live";
	}

	*[Symbol.iterator](): Iterator<unknown> {
		yield this.decision;
		yield this.action;
		yield this.policyResult;
	}
}

/** The server mode of any response this client returned. */
export function modeOf(response: { mode?: string } | RawResponse): string {
	return response.mode ?? "";
}

type Query = Record<string, string | number | undefined | null>;

const seg = (value: string) => encodeURIComponent(value);
const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/** Heimdall bench API client. baseUrl is the server root, e.g. http://127.0.0.1:8090. */
export class Client {
	readonly baseUrl: string;
	readonly token: string | undefined;
	readonly timeoutMs: number;
	readonly requireLive: boolean;
	readonly readRetries: number;
	/** Mode of the last answer, from X-Bench-Mode. */
	lastMode: string | undefined;
	private readonly fetchImpl: typeof fetch;
	/** API path prefix; the v1 client (v1.ts) serves the same transport under /api/v1. */
	protected readonly apiPrefix: string = API_PREFIX;

	constructor(baseUrl: string, options: ClientOptions = {}) {
		this.baseUrl = baseUrl.replace(/\/+$/, "");
		this.token = options.token;
		this.timeoutMs = options.timeoutMs ?? 30_000;
		this.requireLive = options.requireLive ?? false;
		this.readRetries = options.readRetries ?? 2;
		const f = options.fetch ?? globalThis.fetch;
		if (typeof f !== "function") throw new Error("no fetch available: use Node 18+ or pass options.fetch");
		this.fetchImpl = f.bind(globalThis);
	}

	// Health and catalog
	health(): Promise<HealthResponse> { return this.get("/health"); }
	doctor(): Promise<DoctorResponse> { return this.get("/doctor"); }
	catalog(): Promise<CatalogResponse> { return this.get("/catalog"); }
	openapi(): Promise<OpenAPIDocument> { return this.get("/openapi.json"); }

	// Suites and agents
	suites(): Promise<SuitesResponse> { return this.get("/suites"); }
	suiteTask(suite: string, task: string): Promise<SuiteTaskResponse> { return this.get(`/suites/${seg(suite)}/tasks/${seg(task)}`); }
	agents(): Promise<AgentsResponse> { return this.get("/agents"); }

	// Experiments
	experiments(): Promise<ExperimentListResponse> { return this.get("/experiments"); }
	/** Status of one experiment: state, slot progress, spend, analysis id. */
	experiment(expId: string): Promise<ExperimentResponse> { return this.get(`/experiments/${seg(expId)}`); }
	/** Size an experiment; nothing runs. Planner errors come back in `errors`. */
	plan(experiment: ExperimentInput): Promise<PlanResponse> { return this.post("/experiments/plan", experiment); }
	/** Plan, freeze and create the experiment. Nothing runs until start. */
	createExperiment(experiment: ExperimentInput): Promise<ExperimentCreatedResponse> { return this.post("/experiments", experiment); }
	/** Start a created experiment's runs (202 with the status). */
	startExperiment(expId: string): Promise<ExperimentResponse> { return this.post(`/experiments/${seg(expId)}/start`, undefined); }
	/** Create and start in one call. */
	async runExperiment(experiment: ExperimentInput): Promise<ExperimentResponse> {
		const created = await this.createExperiment(experiment);
		return this.startExperiment(created.exp_id);
	}
	/** Live events after `since` and the experiment status (polling form). */
	events(expId: string, since = 0): Promise<EventsPollResponse> { return this.get(`/experiments/${seg(expId)}/events`, { since }); }
	grid(expId: string): Promise<GridResponse> { return this.get(`/experiments/${seg(expId)}/grid`); }
	/** Verdict, gate checklist, config diff and release policy result. */
	verdict(expId: string, gradeSource?: string): Promise<VerdictResponse> { return this.get(`/experiments/${seg(expId)}/verdict`, { grade_source: gradeSource }); }
	/** Diagnostics table, meters and per-arm metrics. */
	analysis(expId: string, gradeSource?: string): Promise<DiagnosticsResponse> { return this.get(`/experiments/${seg(expId)}/diagnostics`, { grade_source: gradeSource }); }
	diagnostics(expId: string, gradeSource?: string): Promise<DiagnosticsResponse> { return this.analysis(expId, gradeSource); }
	triage(expId: string): Promise<TriageResponse> { return this.get(`/experiments/${seg(expId)}/triage`); }

	// Runs
	/** Every slot of an experiment that has a run, from the grid (the API has no run list route). */
	async runs(expId: string): Promise<GridCell[]> {
		const grid = await this.grid(expId);
		return grid.rows.flatMap((row) => row.cells.filter((cell) => Boolean(cell.run_id)));
	}
	run(runId: string): Promise<RunResponse> { return this.get(`/runs/${seg(runId)}`); }
	runEvents(runId: string): Promise<RunEventsResponse> { return this.get(`/runs/${seg(runId)}/events`); }
	runDiff(runId: string): Promise<RunDiffResponse> { return this.get(`/runs/${seg(runId)}/diff`); }
	/** The raw patch as text/plain. */
	runPatch(runId: string): Promise<RawResponse> { return this.raw(`/runs/${seg(runId)}/diff`, { format: "text" }); }
	runReceipts(runId: string): Promise<RunReceiptsResponse> { return this.get(`/runs/${seg(runId)}/receipts`); }
	/** One allowlisted run file (run.json, metrics.json, diff.patch ...). */
	runFile(runId: string, name: string): Promise<RawResponse> { return this.raw(`/runs/${seg(runId)}/files/${seg(name)}`); }
	pair(pairId: string): Promise<PairResponse> { return this.get(`/pairs/${seg(pairId)}`); }

	// Leaderboard, audit, releases
	/** Leaderboard; params: suite, condition, scope, synthetic, grade_source, k. */
	leaderboard(params: Query = {}): Promise<LeaderboardResponse> { return this.get("/leaderboard", params); }
	/** Verdict for a published per-task table of two arms. */
	audit(request: AuditRequest): Promise<AuditResponse> { return this.post("/audit", request); }
	releases(): Promise<ReleaseListResponse> { return this.get("/releases"); }
	release(releaseId: string): Promise<ReleaseResponse> { return this.get(`/releases/${seg(releaseId)}`); }
	/** Record a release decision. `override` is required to release against a block. */
	createRelease(request: ReleaseRequest): Promise<ReleaseResponse> { return this.post("/releases", request); }
	/** The release's evidence pack (zip bytes). */
	releaseEvidence(releaseId: string): Promise<RawResponse> { return this.raw(`/releases/${seg(releaseId)}/evidence`); }

	// Imports and workers
	imports(): Promise<ImportsResponse> { return this.get("/imports"); }
	/** Remote workers; `enabled` is false when worker mode is off. */
	workers(): Promise<WorkersResponse> { return this.get("/workers"); }

	// CI helpers

	/**
	 * Poll until the experiment is done, failed or cancelled; rejects with HeimdallTimeout first.
	 * Each status request is limited to the time left, so an answer after the deadline is a timeout,
	 * never a late success.
	 */
	async waitForExperiment(
		expId: string,
		options: { timeoutMs?: number; intervalMs?: number; onStatus?: (status: ExperimentResponse) => void } = {},
	): Promise<ExperimentResponse> {
		const timeoutMs = options.timeoutMs ?? 3_600_000;
		const intervalMs = options.intervalMs ?? 5_000;
		const deadline = Date.now() + timeoutMs;
		const late = (state: string, mode: string) =>
			new HeimdallTimeout(`experiment ${expId} still ${state} after ${Math.round(timeoutMs / 1000)}s`, mode);
		let last: ExperimentResponse | undefined;
		for (;;) {
			const left = deadline - Date.now();
			if (left <= 0) throw late(last?.state ?? "unknown", last?.mode ?? "");
			let status: ExperimentResponse;
			try {
				status = await this.get<ExperimentResponse>(`/experiments/${seg(expId)}`, undefined, deadline);
			} catch (err) {
				if (err instanceof HeimdallTimeout && Date.now() >= deadline) throw late(last?.state ?? "unknown", last?.mode ?? "");
				throw err;
			}
			if (Date.now() > deadline) throw late(status.state, status.mode);
			last = status;
			options.onStatus?.(status);
			if (TERMINAL_STATES.includes(status.state)) return status;
			const wait = deadline - Date.now();
			if (wait <= 0) throw late(status.state, status.mode);
			await sleep(Math.min(intervalMs, wait));
		}
	}

	/** The release decision for CI: verdict decision, action and policy result. */
	async check(expId: string, gradeSource?: string): Promise<CheckResult> {
		return new CheckResult(await this.verdict(expId, gradeSource));
	}

	// Transport

	protected async get<T>(path: string, query?: Query, deadline?: number): Promise<T> {
		const res = await this.request("GET", path, query, undefined, deadline);
		return this.json<T>(res, "GET", path);
	}

	protected async post<T>(path: string, payload: unknown): Promise<T> {
		const res = await this.request("POST", path, undefined, payload);
		return this.json<T>(res, "POST", path);
	}

	protected async raw(path: string, query?: Query): Promise<RawResponse> {
		const res = await this.request("GET", path, query);
		const mode = res.headers.get(MODE_HEADER) ?? "";
		this.checkMode(mode, "GET", path);
		return new RawResponse(res.body, res.headers.get("Content-Type") ?? "", mode);
	}

	private async json<T>(res: Answer, method: string, path: string): Promise<T> {
		const text = new TextDecoder().decode(res.body);
		if (!text.trim()) return { mode: this.lastMode ?? "" } as T;
		let data: unknown;
		try {
			data = JSON.parse(text);
		} catch (err) {
			throw new HeimdallError(0, `response is not JSON: ${String(err)}`, "decode", this.lastMode, method, path);
		}
		if (!data || typeof data !== "object" || Array.isArray(data)) {
			throw new HeimdallError(0, "response is not a JSON object", "decode", this.lastMode, method, path);
		}
		const obj = data as Record<string, unknown>;
		const mode = String(obj.mode ?? obj["x-bench-mode"] ?? this.lastMode ?? "");
		this.checkMode(mode, method, path);
		return data as T;
	}

	private checkMode(mode: string, method: string, path: string): void {
		if (this.requireLive && mode !== "live") throw new ModeError(mode, method, path);
	}

	/**
	 * Sends one request and reads its whole body under the per-request timeout: a body that stalls
	 * after the headers is a timeout like a late answer, with the same retry rules. deadline (epoch
	 * ms), when given, also caps every attempt and the waits between them.
	 */
	private async request(method: "GET" | "POST", path: string, query?: Query, payload?: unknown, deadline?: number): Promise<Answer> {
		let url = this.baseUrl + this.apiPrefix + path;
		const params = new URLSearchParams();
		for (const [k, v] of Object.entries(query ?? {})) {
			if (v !== undefined && v !== null && v !== "") params.set(k, String(v));
		}
		const qs = params.toString();
		if (qs) url += `?${qs}`;
		const headers: Record<string, string> = { Accept: "application/json" };
		if (this.token) headers.Authorization = `Bearer ${this.token}`;
		let body: string | undefined;
		if (method === "POST") {
			if (typeof payload === "string") {
				body = payload;
				headers["Content-Type"] = "application/yaml";
			} else if (payload !== undefined) {
				body = JSON.stringify(payload);
				headers["Content-Type"] = "application/json";
			}
		}
		const attempts = 1 + (method === "GET" ? this.readRetries : 0);
		for (let attempt = 0; ; attempt++) {
			let timeout = this.timeoutMs;
			if (deadline !== undefined) timeout = Math.min(timeout, deadline - Date.now());
			if (timeout <= 0) throw new HeimdallTimeout(`no answer within ${this.timeoutMs}ms`, "", method, path);
			const last = attempt >= attempts - 1 || (deadline !== undefined && Date.now() + 500 * (attempt + 1) >= deadline);
			const controller = new AbortController();
			const timer = setTimeout(() => controller.abort(), timeout);
			let res: Response;
			let data: Uint8Array;
			try {
				res = await this.fetchImpl(url, { method, headers, body, signal: controller.signal });
				data = new Uint8Array(await res.arrayBuffer());
			} catch (err) {
				const timedOut = controller.signal.aborted;
				if (last) {
					if (timedOut) throw new HeimdallTimeout(`no answer within ${timeout}ms`, "", method, path);
					throw new HeimdallError(0, `cannot reach ${this.baseUrl}: ${String(err)}`, "connection", "", method, path);
				}
				await sleep(500 * (attempt + 1));
				continue;
			} finally {
				clearTimeout(timer);
			}
			this.lastMode = res.headers.get(MODE_HEADER) ?? this.lastMode;
			if (res.ok) return { headers: res.headers, body: data };
			if ((res.status === 502 || res.status === 504) && !last) {
				await sleep(500 * (attempt + 1));
				continue;
			}
			throw httpError(res, data, method, path);
		}
	}
}

/** A response whose body was read in full within the request's timeout. */
interface Answer {
	headers: Headers;
	body: Uint8Array;
}

/** Builds the error from the server's {mode, error: {message, type}} body. */
function httpError(res: Response, body: Uint8Array, method: string, path: string): HeimdallError {
	const text = new TextDecoder().decode(body);
	let message = text.trim();
	let type = "";
	let mode = res.headers.get(MODE_HEADER) ?? "";
	try {
		const data = JSON.parse(text) as { mode?: string; error?: { message?: string; type?: string } };
		if (data && typeof data.error === "object" && data.error) {
			message = String(data.error.message ?? message);
			type = String(data.error.type ?? "");
			mode = data.mode ?? mode;
		}
	} catch {
		// Not JSON: keep the raw text.
	}
	return new HeimdallError(res.status, message || `HTTP ${res.status}`, type, mode, method, path);
}
