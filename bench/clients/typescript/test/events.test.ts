// Unit tests of agent events against a local collector and a temporary file (node:test, node:http).
// Run: node --experimental-strip-types --test test/events.test.ts
import assert from "node:assert/strict";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, afterEach, beforeEach, describe, it } from "node:test";
import {
	EVENTS_FILE_ENV,
	EVENTS_MAX_TEXT,
	EVENTS_STDOUT_PREFIX,
	EVENTS_URL_ENV,
	emit,
	eventsEnabled,
	flushEvents,
	resetEventsForTest,
	stdoutLine,
	step,
} from "../src/index.ts";

const RFC3339_MS = /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/;

type Posted = { url: string; contentType: string; body: string };

async function collector(status = 202): Promise<{ url: string; posted: Posted[]; close: () => Promise<void> }> {
	const posted: Posted[] = [];
	const server: Server = createServer((req: IncomingMessage, res: ServerResponse) => {
		const chunks: Buffer[] = [];
		req.on("data", (c: Buffer) => chunks.push(c));
		req.on("end", () => {
			const body = Buffer.concat(chunks).toString();
			posted.push({ url: req.url ?? "", contentType: String(req.headers["content-type"]), body });
			const n = body.split("\n").filter((l) => l.trim()).length;
			res.writeHead(status, { "Content-Type": "application/json" });
			res.end(JSON.stringify({ accepted: n, dropped: 0 }));
		});
	});
	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	const url = `http://127.0.0.1:${(server.address() as AddressInfo).port}/events`;
	return {
		url,
		posted,
		close: () =>
			new Promise<void>((resolve) => {
				server.closeAllConnections();
				server.close(() => resolve());
			}),
	};
}

function lines(path: string): Record<string, unknown>[] {
	if (!existsSync(path)) return [];
	return readFileSync(path, "utf8")
		.split("\n")
		.filter((l) => l.trim())
		.map((l) => JSON.parse(l) as Record<string, unknown>);
}

const dir = mkdtempSync(join(tmpdir(), "hd-ev-clients-"));
let file = "";
let n = 0;

beforeEach(() => {
	delete process.env[EVENTS_URL_ENV];
	delete process.env[EVENTS_FILE_ENV];
	resetEventsForTest();
	file = join(dir, `run${++n}`, "events.jsonl");
});

afterEach(async () => {
	await flushEvents();
	delete process.env[EVENTS_URL_ENV];
	delete process.env[EVENTS_FILE_ENV];
});

after(() => rmSync(dir, { recursive: true, force: true }));

describe("agent events", () => {
	it("is a no-op that still returns ids when neither variable is set", async () => {
		assert.equal(eventsEnabled(), false);
		const id = emit("plan", "p", { output: "x" });
		assert.match(id, /^[0-9a-f]{32}$/);
		let inner = "";
		const r = await step("tool_call", "t", (s) => {
			inner = s.id;
			s.output = "y";
			return 7;
		});
		assert.equal(r, 7);
		assert.match(inner, /^[0-9a-f]{32}$/);
		assert.notEqual(inner, id);
		assert.equal(emit("plan", undefined, { id: "mine" }), "mine");
		await flushEvents();
		assert.equal(existsSync(file), false);
	});

	it("appends one JSON object per line to BENCH_EVENTS_FILE", async () => {
		process.env[EVENTS_FILE_ENV] = file;
		const plan = emit("plan", "fix", { output: "read, edit, test", attributes: { turn: 1, ok: true, tags: ["a"] } });
		emit("tool_call", "run_tests", { parentId: plan, toolCallId: "call_1", input: { cmd: "npm test" } });
		await flushEvents();
		const got = lines(file);
		assert.equal(got.length, 2);
		assert.equal(got[0]?.type, "plan");
		assert.equal(got[0]?.id, plan);
		assert.deepEqual(got[0]?.attributes, { turn: 1, ok: true, tags: '["a"]' });
		assert.match(String(got[0]?.start_time), RFC3339_MS);
		assert.equal(got[0]?.end_time, undefined);
		assert.equal(got[1]?.parent_id, plan);
		assert.equal(got[1]?.tool_call_id, "call_1");
		assert.deepEqual(got[1]?.input, { cmd: "npm test" });
	});

	it("POSTs JSON lines to BENCH_EVENTS_URL and skips the file when delivered", async () => {
		const col = await collector();
		try {
			process.env[EVENTS_URL_ENV] = col.url;
			process.env[EVENTS_FILE_ENV] = file;
			const id = emit("message", "final", { output: "done", status: "ok" });
			await flushEvents();
			assert.equal(col.posted.length, 1);
			assert.equal(col.posted[0]?.url, "/events");
			assert.equal(col.posted[0]?.contentType, "application/x-ndjson");
			assert.ok(col.posted[0]?.body.endsWith("\n"));
			const ev = JSON.parse(col.posted[0]?.body ?? "") as Record<string, unknown>;
			assert.equal(ev.id, id);
			assert.equal(ev.status, "ok");
			assert.deepEqual(lines(file), []);
		} finally {
			await col.close();
		}
	});

	it("falls back to the file when the POST fails and then skips the URL", async () => {
		const col = await collector(500);
		try {
			process.env[EVENTS_URL_ENV] = col.url;
			process.env[EVENTS_FILE_ENV] = file;
			emit("error", "boom");
			await flushEvents();
			emit("error", "again");
			await flushEvents();
			assert.equal(col.posted.length, 1);
			assert.deepEqual(lines(file).map((e) => e.name), ["boom", "again"]);
		} finally {
			await col.close();
		}
	});

	it("never throws when nothing is reachable or writable", async () => {
		const col = await collector();
		await col.close();
		process.env[EVENTS_URL_ENV] = col.url;
		assert.ok(emit("plan", "x"));
		const blocker = join(dir, "blocker");
		writeFileSync(blocker, "");
		process.env[EVENTS_FILE_ENV] = join(blocker, "x.jsonl");
		assert.ok(emit("plan", "y"));
		await flushEvents();
	});

	it("step times a function, nests by parentId and reports errors", async () => {
		process.env[EVENTS_FILE_ENV] = file;
		let outerId = "";
		let innerId = "";
		await step("subagent_start", "researcher", async (outer) => {
			outerId = outer.id;
			await step(
				"tool_call",
				"search",
				async (inner) => {
					innerId = inner.id;
					inner.output = ["r1", "r2"];
					inner.attributes.hits = 2;
				},
				{ parentId: outer.id, input: { q: "heimdall" } },
			);
		});
		await assert.rejects(
			step("tool_call", "write", () => {
				throw new TypeError("disk full");
			}, { parentId: outerId }),
			TypeError,
		);
		const got = lines(file);
		assert.deepEqual(got.map((e) => e.name), ["search", "researcher", "write"]);
		const [search, researcher, write] = got;
		assert.equal(search?.id, innerId);
		assert.equal(search?.parent_id, outerId);
		assert.deepEqual(search?.output, ["r1", "r2"]);
		assert.equal(search?.status, "ok");
		assert.deepEqual(search?.attributes, { hits: 2 });
		for (const e of got) {
			assert.match(String(e.start_time), RFC3339_MS);
			assert.match(String(e.end_time), RFC3339_MS);
			assert.ok(String(e.start_time) <= String(e.end_time));
		}
		assert.equal(researcher?.id, outerId);
		assert.equal(write?.status, "error");
		assert.equal(write?.output, "TypeError: disk full");
	});

	it("caps input and output at 8 KiB of text", async () => {
		process.env[EVENTS_FILE_ENV] = file;
		emit("tool_result", "big", { output: "é".repeat(10000), input: { blob: "x".repeat(20000) } });
		await flushEvents();
		const [ev] = lines(file);
		assert.equal(typeof ev?.output, "string");
		assert.ok(Buffer.byteLength(String(ev?.output)) <= EVENTS_MAX_TEXT);
		assert.ok(Buffer.byteLength(String(ev?.output)) > EVENTS_MAX_TEXT - 4);
		assert.equal(typeof ev?.input, "string");
		assert.ok(String(ev?.input).startsWith('{"blob":"xxx'));
		assert.ok(Buffer.byteLength(String(ev?.input)) <= EVENTS_MAX_TEXT);
	});

	it("stdoutLine builds one prefixed line", () => {
		const line = stdoutLine("plan", "p", { id: "e1", output: { steps: 3 } });
		assert.ok(line.startsWith(EVENTS_STDOUT_PREFIX));
		assert.ok(!line.includes("\n"));
		const ev = JSON.parse(line.slice(EVENTS_STDOUT_PREFIX.length)) as Record<string, unknown>;
		assert.equal(ev.id, "e1");
		assert.deepEqual(ev.output, { steps: 3 });
		assert.match(String(ev.start_time), RFC3339_MS);
	});
});
