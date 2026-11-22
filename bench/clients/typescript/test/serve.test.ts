// Unit tests of serve() (pull mode) against a local fake server (node:test, node:http).
// Run: node --experimental-strip-types --test test/serve.test.ts
import assert from "node:assert/strict";
import { mkdtempSync, existsSync, readFileSync, rmSync } from "node:fs";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, beforeEach, describe, it } from "node:test";
import { gzipSync } from "node:zlib";
import { PullError, Turn, safeExtract, serve, toAnswer } from "../src/index.ts";

type Answered = { id: string; body: Record<string, unknown>; auth: string };

let server: Server;
let base = "";
let queue: Array<Record<string, unknown>> = [];
let answered: Answered[] = [];
let nextStatus = 200;
let nextBodies: Array<Record<string, unknown>> = [];
let tarball: Buffer = Buffer.alloc(0);

before(async () => {
	server = createServer((req: IncomingMessage, res: ServerResponse) => {
		const chunks: Buffer[] = [];
		req.on("data", (c: Buffer) => chunks.push(c));
		req.on("end", () => {
			const body = Buffer.concat(chunks).toString();
			const send = (status: number, doc?: unknown) => {
				res.writeHead(status, { "Content-Type": "application/json" });
				res.end(doc === undefined ? "" : JSON.stringify(doc));
			};
			if (req.method === "POST" && req.url === "/api/v1/pull/next") {
				nextBodies.push(JSON.parse(body));
				if (nextStatus !== 200) return send(nextStatus, { error: { message: "invalid token" } });
				const t = queue.shift();
				return t ? send(200, t) : setTimeout(() => send(204), 20);
			}
			const m = /^\/api\/v1\/pull\/turns\/([^/]+)\/answer$/.exec(req.url ?? "");
			if (req.method === "POST" && m) {
				answered.push({ id: decodeURIComponent(m[1]!), body: JSON.parse(body), auth: String(req.headers.authorization) });
				return send(204);
			}
			if (req.method === "GET" && req.url === "/api/v1/pull/agents") return send(200, { agents: [{ agent: "bot", connected: true }] });
			if (req.method === "GET" && req.url?.startsWith("/repo")) {
				res.writeHead(200, { "Content-Type": "application/gzip" });
				return res.end(tarball);
			}
			send(404, { error: { message: "not found" } });
		});
	});
	await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
	base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
});

after(() => new Promise<void>((r) => server.close(() => r())));

beforeEach(() => {
	queue = [];
	answered = [];
	nextBodies = [];
	nextStatus = 200;
});

const turnDoc = (id: string, payload: Record<string, unknown> = {}) => ({
	id,
	agent: "bot",
	run_id: "run-1",
	deadline: "2030-01-01T00:00:00Z",
	payload: { kind: "task", instruction: "say hi", session_id: "s1", tools: [{ name: "lookup", description: "find", input_schema: { type: "object" } }], ...payload },
});

describe("serve", () => {
	it("answers a turn with the function's text and stops at maxTurns", async () => {
		queue.push(turnDoc("t1"));
		const seen: Turn[] = [];
		const n = await serve("bot", (t) => (seen.push(t), `hello: ${t.instruction}`), { url: base, token: "tok", maxTurns: 1 });
		assert.equal(n, 1);
		assert.equal(seen[0]!.sessionId, "s1");
		assert.equal(seen[0]!.runId, "run-1");
		assert.deepEqual(seen[0]!.openaiTools()[0]!.function.name, "lookup");
		assert.deepEqual(answered[0], { id: "t1", body: { answer: { text: "hello: say hi" } }, auth: "Bearer tok" });
		assert.equal(nextBodies[0]!.agent, "bot");
		assert.equal(nextBodies[0]!.wait_s, 25);
	});

	it("sends tool calls with ids", async () => {
		queue.push(turnDoc("t2"));
		await serve("bot", async () => ({ toolCalls: [{ name: "lookup", arguments: { q: "x" } }] }), { url: base, token: "tok", maxTurns: 1 });
		const a = answered[0]!.body.answer as { text: string; tool_calls: Array<{ id: string; name: string; arguments: unknown }> };
		assert.equal(a.text, "");
		assert.equal(a.tool_calls[0]!.name, "lookup");
		assert.deepEqual(a.tool_calls[0]!.arguments, { q: "x" });
		assert.match(a.tool_calls[0]!.id, /^call_[0-9a-f]{12}$/);
		assert.equal("toolCalls" in a, false);
	});

	it("fails the turn when the function throws", async () => {
		queue.push(turnDoc("t3"));
		const errs: unknown[] = [];
		await serve("bot", () => {
			throw new TypeError("boom");
		}, { url: base, token: "tok", maxTurns: 1, onError: (_t, e) => errs.push(e) });
		assert.deepEqual(answered[0]!.body, { error: "TypeError: boom" });
		assert.equal(errs.length, 1);
	});

	it("stops with a PullError on 401", async () => {
		nextStatus = 401;
		await assert.rejects(serve("bot", () => "x", { url: base, token: "bad", concurrency: 2 }), (e: unknown) => e instanceof PullError && e.status === 401 && /invalid token/.test(e.message));
	});

	it("stops when the signal aborts", async () => {
		const ctl = new AbortController();
		setTimeout(() => ctl.abort(), 80);
		assert.equal(await serve("bot", () => "x", { url: base, token: "tok", signal: ctl.signal }), 0);
	});

	it("needs a URL and a token", async () => {
		await assert.rejects(serve("bot", () => "x", { url: "", token: "" }), /serve needs the Heimdall URL/);
	});
});

describe("toAnswer", () => {
	it("maps return values", () => {
		assert.deepEqual(toAnswer(undefined), { text: "" });
		assert.deepEqual(toAnswer({ patch: "diff", usage: { input_tokens: 1 } }), { text: "", patch: "diff", usage: { input_tokens: 1 } });
		assert.throws(() => toAnswer(42), TypeError);
	});
});

// tarEntry builds one ustar entry (header plus padded body).
function tarEntry(name: string, data: string, type = "0"): Buffer {
	const h = Buffer.alloc(512);
	h.write(name, 0, 100);
	h.write("0000644\0", 100);
	h.write("0000000\0", 108);
	h.write("0000000\0", 116);
	h.write(data.length.toString(8).padStart(11, "0") + "\0", 124);
	h.write("00000000000\0", 136);
	h.write("        ", 148);
	h.write(type, 156);
	h.write("ustar\0", 257);
	h.write("00", 263);
	let sum = 0;
	for (const b of h) sum += b;
	h.write(sum.toString(8).padStart(6, "0") + "\0 ", 148);
	const body = Buffer.alloc(Math.ceil(data.length / 512) * 512);
	body.write(data);
	return Buffer.concat([h, body]);
}

describe("downloadRepo", () => {
	it("unpacks files and refuses paths outside dest and links", async () => {
		tarball = gzipSync(Buffer.concat([
			tarEntry("src/", "", "5"),
			tarEntry("src/main.py", "print(1)\n"),
			tarEntry("../evil.txt", "no"),
			tarEntry("src/link", "", "2"),
			Buffer.alloc(1024),
		]));
		const dir = mkdtempSync(join(tmpdir(), "heimdall-serve-"));
		try {
			const dest = join(dir, "repo");
			const turn = new Turn(turnDoc("t4", { repo: { url: `${base}/repo?exp=1&sig=x` } }));
			const files = await turn.downloadRepo(dest);
			assert.deepEqual(files, [join("src", "main.py")]);
			assert.equal(readFileSync(join(dest, "src", "main.py"), "utf8"), "print(1)\n");
			assert.equal(existsSync(join(dir, "evil.txt")), false);
			assert.equal(existsSync(join(dest, "src", "link")), false);
		} finally {
			rmSync(dir, { recursive: true, force: true });
		}
	});

	it("refuses a turn without a repository", async () => {
		await assert.rejects(new Turn(turnDoc("t5")).downloadRepo("/nonexistent"), /carries no repository/);
		await assert.rejects(safeExtract(new Uint8Array([1, 2, 3]), join(tmpdir(), "x-never")), Error);
	});
});
