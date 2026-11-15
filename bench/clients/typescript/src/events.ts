/**
 * Agent events: an agent reports its own internal steps to the Heimdall bench.
 *
 *   import { emit, step, flushEvents } from "@heimdall/bench-client";
 *   const plan = emit("plan", "fix the failing test", { output: "read, edit, run the tests" });
 *   await step("tool_call", "run_tests", async (s) => { s.output = await runTests(); }, { parentId: plan });
 *   await flushEvents(); // before the process exits
 *
 * The wire format is spec/agent-event.schema.json. Delivery follows the
 * environment the bench sets in the agent container: BENCH_EVENTS_URL (POST,
 * JSON lines), else BENCH_EVENTS_FILE (one JSON object appended per line). A
 * failed POST falls back to the file when both are set. With neither set every
 * call is a no-op that still returns an event id. Nothing here throws into the
 * agent. stdoutLine builds the "@@heimdall-event " line for an agent that can
 * only print.
 *
 * Everything an agent reports is its own word: the bench shows it as
 * agent-reported evidence, which never raises a trusted metric or decides an
 * outcome. No dependencies: Node 18+ (global fetch, node:fs).
 */

export const EVENTS_URL_ENV = "BENCH_EVENTS_URL";
export const EVENTS_FILE_ENV = "BENCH_EVENTS_FILE";
export const EVENTS_STDOUT_PREFIX = "@@heimdall-event ";

/** Input and output are sent as JSON while their text fits this many bytes, and as text cut to it otherwise. */
export const EVENTS_MAX_TEXT = 8192;
const POST_TIMEOUT_MS = 2000;
// After a failed POST the URL is skipped for this long, so an unreachable
// collector costs the agent one timeout, not one per event.
const URL_RETRY_MS = 30_000;

export type ReportedEventType =
	| "plan"
	| "thought_summary"
	| "tool_call"
	| "tool_result"
	| "subagent_start"
	| "subagent_end"
	| "handoff"
	| "memory_read"
	| "memory_write"
	| "retrieval"
	| "message"
	| "error"
	| "custom";

export type ReportedEventStatus = "ok" | "error" | "started" | "unknown";

/** One event on the wire (spec/agent-event.schema.json). */
export interface ReportedEvent {
	type: ReportedEventType | (string & {});
	id: string;
	parent_id?: string;
	name?: string;
	tool_call_id?: string;
	input?: unknown;
	output?: unknown;
	status?: ReportedEventStatus;
	start_time?: string | number;
	end_time?: string | number;
	attributes?: Record<string, string | number | boolean>;
}

/** Optional fields of an event. Times are RFC 3339 strings, Unix milliseconds or Dates. */
export interface EmitOptions {
	id?: string;
	parentId?: string;
	toolCallId?: string;
	input?: unknown;
	output?: unknown;
	status?: ReportedEventStatus;
	startTime?: string | number | Date;
	endTime?: string | number | Date;
	attributes?: Record<string, unknown>;
}

/** What step() hands its function: the event id, and output, status and attributes to set before it returns. */
export interface StepHandle {
	readonly id: string;
	output?: unknown;
	status?: ReportedEventStatus;
	attributes: Record<string, unknown>;
}

type Env = Record<string, string | undefined>;
type FsLike = {
	appendFileSync(path: string, data: string): void;
	mkdirSync(path: string, options: { recursive: boolean }): unknown;
};
type ProcessLike = { env?: Env; getBuiltinModule?: (id: string) => unknown };

const NODE_FS: string = "node:fs";
const pending = new Set<Promise<void>>();
let urlDownUntil = 0;
let fsModule: FsLike | null | undefined;
let fsLoading: Promise<FsLike | null> | undefined;
let fileQueue: Promise<void> = Promise.resolve();
let queuedWrites = 0;

function proc(): ProcessLike | undefined {
	return (globalThis as { process?: ProcessLike }).process;
}

function env(name: string): string {
	return proc()?.env?.[name] ?? "";
}

/** True when the bench asked for events (either variable is set). */
export function eventsEnabled(): boolean {
	return env(EVENTS_URL_ENV) !== "" || env(EVENTS_FILE_ENV) !== "";
}

/** A fresh event id: 32 hex characters. */
export function newEventId(): string {
	const c = (globalThis as { crypto?: { randomUUID?: () => string } }).crypto;
	if (typeof c?.randomUUID === "function") return c.randomUUID().replaceAll("-", "");
	let s = "";
	for (let i = 0; i < 32; i++) s += Math.floor(Math.random() * 16).toString(16);
	return s;
}

/** The current UTC time as RFC 3339 with milliseconds, e.g. 2026-11-15T13:04:05.123Z. */
export function eventTime(): string {
	return new Date().toISOString();
}

function time(v: string | number | Date): string | number {
	return v instanceof Date ? v.toISOString() : v;
}

function utf8Cut(text: string, max: number): string {
	const raw = new TextEncoder().encode(text);
	if (raw.length <= max) return text;
	// fatal: false drops a character cut in half (it decodes to U+FFFD, removed below).
	return new TextDecoder("utf-8", { fatal: false }).decode(raw.slice(0, max)).replace(/�+$/, "");
}

function cap(v: unknown): unknown {
	let text: string;
	let value: unknown;
	if (typeof v === "string") {
		text = v;
		value = v;
	} else {
		try {
			text = JSON.stringify(v, (_k, x: unknown) => (typeof x === "bigint" ? x.toString() : x)) ?? String(v);
			value = JSON.parse(text) as unknown;
		} catch {
			text = String(v);
			value = text;
		}
	}
	if (new TextEncoder().encode(text).length <= EVENTS_MAX_TEXT) return value;
	return utf8Cut(text, EVENTS_MAX_TEXT);
}

function scalar(v: unknown): string | number | boolean {
	if (typeof v === "string" || typeof v === "number" || typeof v === "boolean") return v;
	try {
		return JSON.stringify(v) ?? String(v);
	} catch {
		return String(v);
	}
}

/**
 * The event object emit sends, with an id and a start_time filled in when
 * missing. A start_time is set to now when neither time is given, so an event
 * read later from the file keeps its own time.
 */
export function buildEvent(type: ReportedEventType | (string & {}), name?: string, opts: EmitOptions = {}): ReportedEvent {
	const ev: ReportedEvent = { type: String(type), id: opts.id ? String(opts.id) : newEventId() };
	if (opts.parentId) ev.parent_id = String(opts.parentId);
	if (name !== undefined && name !== null) ev.name = String(name);
	if (opts.toolCallId) ev.tool_call_id = String(opts.toolCallId);
	if (opts.input !== undefined && opts.input !== null) ev.input = cap(opts.input);
	if (opts.output !== undefined && opts.output !== null) ev.output = cap(opts.output);
	if (opts.status) ev.status = opts.status;
	let start = opts.startTime;
	if (start === undefined && opts.endTime === undefined) start = eventTime();
	if (start !== undefined) ev.start_time = time(start);
	if (opts.endTime !== undefined) ev.end_time = time(opts.endTime);
	if (opts.attributes && Object.keys(opts.attributes).length > 0) {
		ev.attributes = {};
		for (const [k, v] of Object.entries(opts.attributes)) {
			if (v !== undefined && v !== null) ev.attributes[k] = scalar(v);
		}
	}
	return ev;
}

/**
 * Report one event and return its id (generated when not given). Delivery
 * runs in the background: await flushEvents() before the process exits. A
 * no-op when neither BENCH_EVENTS_URL nor BENCH_EVENTS_FILE is set. Never
 * throws: a failed delivery is dropped silently.
 */
export function emit(type: ReportedEventType | (string & {}), name?: string, opts: EmitOptions = {}): string {
	return deliver(type, name, opts).id;
}

function deliver(type: string, name: string | undefined, opts: EmitOptions): { id: string; done: Promise<void> } {
	let ev: ReportedEvent;
	try {
		ev = buildEvent(type, name, opts);
	} catch {
		return { id: opts.id ? String(opts.id) : newEventId(), done: Promise.resolve() };
	}
	if (!eventsEnabled()) return { id: ev.id, done: Promise.resolve() };
	let line: string;
	try {
		line = JSON.stringify(ev);
	} catch {
		return { id: ev.id, done: Promise.resolve() };
	}
	const done = send(line).catch(() => undefined);
	pending.add(done);
	void done.finally(() => pending.delete(done));
	return { id: ev.id, done };
}

/** The stdout line that carries one event: "@@heimdall-event " then the JSON object, no newline. It sends nothing itself. */
export function stdoutLine(type: ReportedEventType | (string & {}), name?: string, opts: EmitOptions = {}): string {
	return EVENTS_STDOUT_PREFIX + JSON.stringify(buildEvent(type, name, opts));
}

/**
 * Run fn and report it as one event when it settles, with its start and end
 * time and status ok, or status error when it throws (the error is
 * rethrown). Resolves to fn's result once the event is delivered (at most the
 * POST timeout). Nest steps by passing the outer handle's id as parentId.
 */
export async function step<T>(
	type: ReportedEventType | (string & {}),
	name: string | undefined,
	fn: (s: StepHandle) => T | Promise<T>,
	opts: EmitOptions = {},
): Promise<T> {
	const s: StepHandle = { id: opts.id || newEventId(), output: opts.output, status: opts.status, attributes: { ...opts.attributes } };
	const startTime = opts.startTime ?? eventTime();
	let result: T;
	try {
		result = await fn(s);
	} catch (err) {
		const output = s.output ?? (err instanceof Error ? `${err.name}: ${err.message}` : String(err));
		await deliver(type, name, { ...opts, id: s.id, output, status: "error", startTime, endTime: eventTime(), attributes: s.attributes }).done;
		throw err;
	}
	await deliver(type, name, { ...opts, id: s.id, output: s.output, status: s.status ?? "ok", startTime, endTime: eventTime(), attributes: s.attributes }).done;
	return result;
}

/** Wait until every event sent so far is delivered or dropped. */
export async function flushEvents(): Promise<void> {
	while (pending.size > 0) await Promise.all([...pending]);
}

async function send(line: string): Promise<void> {
	const url = env(EVENTS_URL_ENV);
	const path = env(EVENTS_FILE_ENV);
	if (url && (await post(url, line))) return;
	if (path) await appendLine(path, line);
}

async function post(url: string, line: string): Promise<boolean> {
	if (Date.now() < urlDownUntil) return false;
	try {
		const resp = await fetch(url, {
			method: "POST",
			headers: { "Content-Type": "application/x-ndjson" },
			body: line + "\n",
			signal: AbortSignal.timeout(POST_TIMEOUT_MS),
		});
		await resp.arrayBuffer().catch(() => undefined);
		if (resp.ok) return true;
	} catch {
		// unreachable, refused or timed out: fall through to the file
	}
	urlDownUntil = Date.now() + URL_RETRY_MS;
	return false;
}

function syncFs(): FsLike | null | undefined {
	if (fsModule !== undefined) return fsModule;
	const p = proc();
	if (!p) return (fsModule = null);
	if (typeof p.getBuiltinModule === "function") {
		try {
			return (fsModule = (p.getBuiltinModule(NODE_FS) as FsLike | undefined) ?? null);
		} catch {
			return (fsModule = null);
		}
	}
	return undefined; // Node before 20.16: load it asynchronously
}

function asyncFs(): Promise<FsLike | null> {
	fsLoading ??= import(NODE_FS).then(
		(m: unknown) => (fsModule = m as FsLike),
		() => (fsModule = null),
	);
	return fsLoading;
}

function writeWith(fs: FsLike | null, path: string, line: string): void {
	if (!fs) return;
	try {
		const slash = path.lastIndexOf("/");
		if (slash > 0) fs.mkdirSync(path.slice(0, slash), { recursive: true });
		// One appendFileSync of the whole line: lines never interleave.
		fs.appendFileSync(path, line + "\n");
	} catch {
		// unwritable: the event is dropped
	}
}

function appendLine(path: string, line: string): Promise<void> {
	const fs = syncFs();
	if (fs !== undefined && queuedWrites === 0) {
		writeWith(fs, path, line);
		return Promise.resolve();
	}
	// fs is still loading: queue the write so lines keep their order.
	queuedWrites++;
	fileQueue = fileQueue
		.then(async () => writeWith(fs !== undefined ? fs : await asyncFs(), path, line))
		.finally(() => {
			queuedWrites--;
		});
	return fileQueue;
}

/** Forget a failed URL (for tests). */
export function resetEventsForTest(): void {
	urlDownUntil = 0;
}
