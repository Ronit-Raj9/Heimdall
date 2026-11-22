/**
 * Serve your own agent function to Heimdall: pull mode (docs/pull-agents.md).
 *
 * Your agent runs in your own process and connects out to Heimdall over
 * HTTPS; nothing connects in to your network. Register an agent with adapter
 * `pull` (`pull: {name: support-bot}`), start an evaluation, and run:
 *
 *     import { serve } from "@heimdall/bench-client";
 *     await serve("support-bot", async (turn) => myAgent(turn.instruction), {
 *         url: "https://heimdall.example.com", token: process.env.HEIMDALL_TOKEN, concurrency: 4 });
 *
 * Each turn is one call of `fn`. Return a string (the agent's text), or a
 * PullAnswer with `text`, `toolCalls` / `tool_calls` (Heimdall runs them through
 * the task's tools and sends their results back as the next turn, kind
 * "tool_results"), `patch` (a unified diff, for coding tasks) and `usage`.
 * A thrown error fails the turn with its message.
 *
 * The same wire protocol as the Python SDK (clients/python/heimdall/serve.py).
 * No dependencies: Node 18+ (global fetch; node:fs, node:path and node:zlib
 * only for Turn.downloadRepo).
 */

export const PULL_PREFIX = "/api/v1/pull";
export const MAX_REPO_BYTES = 48 << 20;

/** The routes serve() calls, as [method, OpenAPI path]. */
export const SERVE_ROUTES: ReadonlyArray<readonly [string, string]> = [
	["POST", "/api/v1/pull/next"],
	["POST", "/api/v1/pull/turns/{id}/answer"],
	["GET", "/api/v1/pull/agents"],
];

/** A tool call for Heimdall to run through the task's tools. */
export interface PullToolCall {
	id?: string;
	name: string;
	arguments?: Record<string, unknown>;
}

/** What a turn answers. */
export interface PullAnswer {
	text?: string;
	toolCalls?: PullToolCall[];
	tool_calls?: PullToolCall[];
	patch?: string;
	usage?: { input_tokens?: number; output_tokens?: number };
	version?: string;
}

/** A tool of the task, as the turn offers it. */
export interface PullTool {
	name: string;
	description?: string;
	input_schema?: Record<string, unknown>;
}

/** One served agent of the organization (GET /pull/agents). */
export type PullAgentStatus = Record<string, unknown> & { agent?: string; connected?: boolean };

/** Heimdall refused a pull request. */
export class PullError extends Error {
	readonly status: number;
	constructor(status: number, message: string) {
		super(`HTTP ${status}: ${message}`);
		this.name = "PullError";
		this.status = status;
	}
}

type Doc = Record<string, unknown>;
const str = (v: unknown, d = ""): string => (typeof v === "string" ? v : d);

/**
 * One unit of work for your agent. kind: "task" (the task's first turn),
 * "conversation" (a customer message when Heimdall drives a chat: answer with
 * your reply) or "tool_results" (the results of the tool calls you asked for
 * are the last messages).
 */
export class Turn {
	readonly id: string;
	readonly agent: string;
	readonly runId: string;
	readonly deadline: string;
	readonly payload: Doc;
	readonly kind: string;
	readonly taskId: string;
	readonly sessionId: string;
	readonly sessionIndex: number;
	readonly output: string;
	readonly instruction: string;
	readonly message: string | undefined;
	readonly turn: number;
	readonly messages: Doc[];
	readonly tools: PullTool[];
	readonly repo: { url?: string; [k: string]: unknown } | undefined;
	private readonly fetchImpl: typeof fetch;

	constructor(doc: Doc, fetchImpl: typeof fetch = fetch) {
		const p = (doc.payload && typeof doc.payload === "object" ? doc.payload : {}) as Doc;
		this.fetchImpl = fetchImpl;
		this.id = str(doc.id);
		this.agent = str(doc.agent);
		this.runId = str(doc.run_id);
		this.deadline = str(doc.deadline);
		this.payload = p;
		this.kind = str(p.kind, "task");
		this.taskId = str(p.task_id);
		this.sessionId = str(p.session_id);
		this.sessionIndex = Number(p.session_index) || 1;
		this.output = str(p.output, "answer");
		this.instruction = str(p.instruction);
		this.message = typeof p.message === "string" ? p.message : undefined;
		this.turn = Number(p.turn) || 0;
		this.messages = Array.isArray(p.messages) ? (p.messages as Doc[]) : [];
		this.tools = Array.isArray(p.tools) ? (p.tools as PullTool[]) : [];
		this.repo = p.repo && typeof p.repo === "object" ? (p.repo as Turn["repo"]) : undefined;
	}

	/** The task's tools as OpenAI chat function tools. */
	openaiTools(): Array<{ type: "function"; function: { name: string; description: string; parameters: Record<string, unknown> } }> {
		return this.tools.map((t) => ({
			type: "function" as const,
			function: { name: t.name, description: t.description ?? "", parameters: t.input_schema ?? { type: "object", properties: {} } },
		}));
	}

	/**
	 * Downloads the task's repository tarball (coding tasks) and unpacks it
	 * into dest, refusing links and paths that leave dest. Returns the files
	 * written. Make your change there and answer `{patch: <git diff>}`.
	 */
	async downloadRepo(dest: string): Promise<string[]> {
		const url = this.repo?.url;
		if (!url) throw new Error("this turn carries no repository (not a coding task, or not its first turn)");
		const resp = await this.fetchImpl(url, { headers: { Accept: "application/gzip" } });
		if (!resp.ok) throw new PullError(resp.status, (await resp.text()).slice(0, 300));
		const data = new Uint8Array(await resp.arrayBuffer());
		if (data.length > MAX_REPO_BYTES) throw new Error(`the repository tarball is larger than ${MAX_REPO_BYTES} bytes`);
		return safeExtract(data, dest);
	}
}

const NODE_ZLIB: string = "node:zlib";
const NODE_FS: string = "node:fs";
const NODE_PATH: string = "node:path";

/**
 * Unpacks a tar.gz into dest: regular files and folders only (ustar, with
 * GNU long names and pax path records), nothing outside dest.
 */
export async function safeExtract(gz: Uint8Array, dest: string): Promise<string[]> {
	const zlib = await import(NODE_ZLIB);
	const fs = await import(NODE_FS);
	const path = await import(NODE_PATH);
	const tar: Uint8Array = zlib.gunzipSync(gz, { maxOutputLength: MAX_REPO_BYTES * 4 });
	fs.mkdirSync(dest, { recursive: true });
	const root: string = fs.realpathSync(dest);
	const dec = new TextDecoder();
	const field = (b: Uint8Array, off: number, len: number): string => {
		const s = dec.decode(b.subarray(off, off + len));
		const nul = s.indexOf("\0");
		return nul < 0 ? s : s.slice(0, nul);
	};
	const written: string[] = [];
	let pos = 0;
	let longName = "";
	while (pos + 512 <= tar.length) {
		const h = tar.subarray(pos, pos + 512);
		if (h.every((x) => x === 0)) break;
		const size = parseInt(field(h, 124, 12).trim() || "0", 8) || 0;
		const mode = parseInt(field(h, 100, 8).trim() || "0", 8) || 0;
		const type = String.fromCharCode(h[156] ?? 0);
		const prefix = field(h, 345, 155);
		const name = longName || (prefix ? `${prefix}/${field(h, 0, 100)}` : field(h, 0, 100));
		longName = "";
		const body = tar.subarray(pos + 512, pos + 512 + size);
		pos += 512 + Math.ceil(size / 512) * 512;
		if (type === "L") {
			longName = field(body, 0, body.length);
			continue;
		}
		if (type === "x") {
			const m = /(?:^|\n)\d+ path=([^\n]*)\n/.exec(dec.decode(body));
			if (m?.[1]) longName = m[1];
			continue;
		}
		if (type === "g") continue;
		const isFile = type === "0" || type === "\0" || type === "7";
		const isDir = type === "5";
		if (!isFile && !isDir) continue;
		const target: string = path.resolve(root, name);
		if (target !== root && !target.startsWith(root + path.sep)) continue;
		if (isDir) {
			fs.mkdirSync(target, { recursive: true });
			continue;
		}
		const parent: string = path.dirname(target);
		fs.mkdirSync(parent, { recursive: true });
		// A folder symlink planted earlier cannot exist (links are skipped), but
		// check the real parent anyway.
		const realParent: string = fs.realpathSync(parent);
		if (realParent !== root && !realParent.startsWith(root + path.sep)) continue;
		fs.writeFileSync(target, body);
		fs.chmodSync(target, mode & 0o111 ? 0o755 : 0o644);
		written.push(path.relative(root, target));
	}
	return written;
}

/** The answer object a function's return value turns into. */
export function toAnswer(value: unknown): Doc {
	if (value === undefined || value === null) return { text: "" };
	if (typeof value === "string") return { text: value };
	if (typeof value !== "object" || Array.isArray(value)) {
		throw new TypeError(`an agent function returns a string or a PullAnswer, not ${Array.isArray(value) ? "array" : typeof value}`);
	}
	const a = value as PullAnswer & Doc;
	const out: Doc = {};
	for (const [k, v] of Object.entries(a)) if (k !== "toolCalls" && v !== undefined) out[k] = v;
	const calls = a.toolCalls ?? a.tool_calls;
	if (calls && calls.length > 0) {
		out.tool_calls = calls.map((c) => ({ id: c.id || `call_${randomHex(12)}`, name: c.name, arguments: c.arguments ?? {} }));
	} else {
		delete out.tool_calls;
	}
	if (out.text === undefined) out.text = "";
	return out;
}

function randomHex(n: number): string {
	let s = "";
	for (let i = 0; i < n; i++) s += Math.floor(Math.random() * 16).toString(16);
	return s;
}

export interface ServeOptions {
	/** Heimdall URL; default HEIMDALL_URL. */
	url?: string;
	/** An org API token (scope evaluate or admin), or a local server's write token; default HEIMDALL_TOKEN. */
	token?: string;
	/** Turns handled at once; default 1. */
	concurrency?: number;
	/** Stop after this many turns. */
	maxTurns?: number;
	/** Stop when aborted. */
	signal?: AbortSignal;
	/** Called when fn throws (the turn is failed with the error's message). */
	onError?: (turn: Turn, err: unknown) => void;
	log?: (msg: string) => void;
	fetch?: typeof fetch;
}

function env(name: string): string {
	const g = globalThis as { process?: { env?: Record<string, string | undefined> } };
	return g.process?.env?.[name] ?? "";
}

class Conn {
	readonly url: string;
	readonly token: string;
	readonly fetchImpl: typeof fetch;
	constructor(url: string, token: string, fetchImpl: typeof fetch) {
		this.url = url;
		this.token = token;
		this.fetchImpl = fetchImpl;
	}

	async call(method: string, path: string, body?: unknown, timeoutMs = 60_000, signal?: AbortSignal): Promise<{ status: number; doc: Doc }> {
		const ctl = new AbortController();
		const timer = setTimeout(() => ctl.abort(), timeoutMs);
		const onAbort = () => ctl.abort();
		signal?.addEventListener("abort", onAbort, { once: true });
		try {
			const headers: Record<string, string> = { Authorization: `Bearer ${this.token}`, Accept: "application/json", "User-Agent": "heimdall-serve-typescript" };
			if (body !== undefined) headers["Content-Type"] = "application/json";
			const init: RequestInit = { method, headers, signal: ctl.signal };
			if (body !== undefined) init.body = JSON.stringify(body);
			const resp = await this.fetchImpl(this.url.replace(/\/+$/, "") + path, init);
			const raw = await resp.text();
			if (!resp.ok) {
				let msg = raw.slice(0, 300);
				try {
					const m = (JSON.parse(raw) as { error?: { message?: string } }).error?.message;
					if (m) msg = m;
				} catch {
					// not JSON: keep the raw text
				}
				throw new PullError(resp.status, msg);
			}
			return { status: resp.status, doc: raw.trim() ? (JSON.parse(raw) as Doc) : {} };
		} finally {
			clearTimeout(timer);
			signal?.removeEventListener("abort", onAbort);
		}
	}
}

const sleep = (ms: number, signal?: AbortSignal): Promise<void> =>
	new Promise((resolve) => {
		if (signal?.aborted) return resolve();
		const t = setTimeout(resolve, ms);
		signal?.addEventListener("abort", () => (clearTimeout(t), resolve()), { once: true });
	});

/**
 * Takes turns of `agent` from Heimdall and answers each with `fn` until
 * `signal` aborts or `maxTurns` turns were answered. Resolves to the number of
 * turns handled; rejects with a PullError on 401, 403 or 404 (a bad token or
 * agent), after stopping every worker.
 */
export async function serve(
	agent: string,
	fn: (turn: Turn) => unknown | Promise<unknown>,
	opts: ServeOptions = {},
): Promise<number> {
	const url = opts.url || env("HEIMDALL_URL");
	const token = opts.token || env("HEIMDALL_TOKEN");
	if (!url || !token) throw new Error("serve needs the Heimdall URL and an API token (url, token or HEIMDALL_URL, HEIMDALL_TOKEN)");
	const say = opts.log ?? (() => {});
	const fetchImpl = opts.fetch ?? fetch;
	const conn = new Conn(url, token, fetchImpl);
	const stop = new AbortController();
	const onOuter = () => stop.abort();
	if (opts.signal?.aborted) stop.abort();
	opts.signal?.addEventListener("abort", onOuter, { once: true });
	const client = `node-${randomHex(8)}`;
	let handled = 0;
	let fatal: unknown;

	const takeOne = async (): Promise<void> => {
		let res: { status: number; doc: Doc };
		try {
			res = await conn.call("POST", `${PULL_PREFIX}/next`, { agent, client, wait_s: 25 }, 60_000, stop.signal);
		} catch (e) {
			if (stop.signal.aborted) return;
			if (e instanceof PullError && [401, 403, 404].includes(e.status)) throw e;
			say(`next: ${e instanceof Error ? e.message : String(e)}`);
			await sleep(2000, stop.signal);
			return;
		}
		if (res.status === 204 || !res.doc.id) return;
		const turn = new Turn(res.doc, fetchImpl);
		let body: Doc;
		try {
			body = { answer: toAnswer(await fn(turn)) };
		} catch (e) {
			opts.onError?.(turn, e);
			const name = e instanceof Error ? e.name : "Error";
			body = { error: `${name}: ${e instanceof Error ? e.message : String(e)}` };
		}
		for (let attempt = 0; attempt < 3; attempt++) {
			try {
				await conn.call("POST", `${PULL_PREFIX}/turns/${encodeURIComponent(turn.id)}/answer`, body);
				break;
			} catch (e) {
				say(`answer ${turn.id}: ${e instanceof Error ? e.message : String(e)}`);
				if (e instanceof PullError) break;
				await sleep(1000 * (1 + attempt));
			}
		}
		handled++;
		if (opts.maxTurns !== undefined && handled >= opts.maxTurns) stop.abort();
	};

	const worker = async (): Promise<void> => {
		while (!stop.signal.aborted) {
			try {
				await takeOne();
			} catch (e) {
				fatal ??= e;
				stop.abort();
			}
		}
	};

	const n = Math.max(1, opts.concurrency ?? 1);
	say(`serving ${agent} with ${n} worker(s)`);
	try {
		await Promise.all(Array.from({ length: n }, worker));
	} finally {
		opts.signal?.removeEventListener("abort", onOuter);
	}
	if (fatal) throw fatal;
	return handled;
}

/** The organization's pull agents and whether a process serves each. */
export async function pullAgents(opts: Pick<ServeOptions, "url" | "token" | "fetch"> = {}): Promise<PullAgentStatus[]> {
	const conn = new Conn(opts.url || env("HEIMDALL_URL"), opts.token || env("HEIMDALL_TOKEN"), opts.fetch ?? fetch);
	const { doc } = await conn.call("GET", `${PULL_PREFIX}/agents`, undefined, 30_000);
	return Array.isArray(doc.agents) ? (doc.agents as PullAgentStatus[]) : [];
}
