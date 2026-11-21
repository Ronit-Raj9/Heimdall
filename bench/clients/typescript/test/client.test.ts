// Unit tests of the client against a local fake server (node:test, node:http).
// Run: node --experimental-strip-types --test test/client.test.ts
import assert from "node:assert/strict";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { after, before, beforeEach, describe, it } from "node:test";
import { Client, HeimdallError, HeimdallTimeout, ModeError, modeOf } from "../src/index.ts";

type Reply = { status: number; headers: Record<string, string>; body: string };
type Recorded = { method: string; url: string; headers: IncomingMessage["headers"]; body: string };

const jsonReply = (status: number, payload: Record<string, unknown>, mode = "live"): Reply => ({
	status,
	headers: { "Content-Type": "application/json; charset=utf-8", "X-Bench-Mode": mode },
	body: JSON.stringify({ mode, ...payload }),
});
const errorReply = (status: number, type: string, message: string, mode = "live"): Reply =>
	jsonReply(status, { error: { message, type } }, mode);

let server: Server;
let base = "";
let routes = new Map<string, Array<() => Reply | Promise<Reply>>>();
let requests: Recorded[] = [];

function on(method: string, url: string, ...replies: Reply[]): void {
	routes.set(`${method} ${url}`, replies.map((r) => () => r));
}

before(async () => {
	server = createServer((req: IncomingMessage, res: ServerResponse) => {
		const chunks: Buffer[] = [];
		req.on("data", (c: Buffer) => chunks.push(c));
		req.on("end", async () => {
			requests.push({ method: req.method ?? "", url: req.url ?? "", headers: req.headers, body: Buffer.concat(chunks).toString() });
			const queue = routes.get(`${req.method} ${req.url}`);
			const reply = queue && queue.length > 0 ? await (queue.length > 1 ? queue.shift()! : queue[0]!)() : errorReply(404, "not_found", `route not found: ${req.url}`);
			if (res.destroyed) return;
			res.writeHead(reply.status, reply.headers);
			res.end(reply.body);
		});
	});
	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
});

after(() => {
	server.closeAllConnections();
	server.close();
});

beforeEach(() => {
	routes = new Map();
	requests = [];
});

const statusPayload = (state: string) => ({
	exp_id: "e1", name: "smoke", state, manifest_hash: "h", frozen_at: "2026-11-15T00:00:00Z",
	slots: { total: 2, done: 1, running: 1, failed: 0 }, spent_usd: { state: "unknown", evidence: "gateway" },
});
const verdictPayload = (policy?: string) => ({
	exp_id: "e1", analysis_id: "a1", grade_source: "original", suite: "s", naive: {}, gates: [], config_diff: [],
	verdict: { decision: "regressed", action: "hold" },
	...(policy ? { policy: { policy_name: "p", policy_hash: "x", decision: policy, checks: [], evaluated_at: "2026-11-15T00:00:00Z" } } : {}),
});

describe("Client", () => {
	it("exposes the server mode on every response", async () => {
		on("GET", "/api/bench/health", jsonReply(200, { status: "ok", version: "1" }, "recorded"));
		const c = new Client(base);
		const h = await c.health();
		assert.equal(h.mode, "recorded");
		assert.equal(modeOf(h), "recorded");
		assert.equal(c.lastMode, "recorded");
	});

	it("sends the bearer token only when set", async () => {
		on("GET", "/api/bench/agents", jsonReply(200, { agents: [] }));
		await new Client(base, { token: "tok" }).agents();
		assert.equal(requests.at(-1)?.headers.authorization, "Bearer tok");
		await new Client(base).agents();
		assert.equal(requests.at(-1)?.headers.authorization, undefined);
	});

	it("turns the error shape into HeimdallError", async () => {
		on("GET", "/api/bench/experiments/nope", errorReply(404, "not_found", "experiment not found: nope", "recorded"));
		await assert.rejects(new Client(base).experiment("nope"), (err: unknown) => {
			assert.ok(err instanceof HeimdallError);
			assert.deepEqual([err.status, err.type, err.serverMessage, err.mode], [404, "not_found", "experiment not found: nope", "recorded"]);
			return true;
		});
	});

	it("plans from an object as JSON and from text as YAML", async () => {
		on("POST", "/api/bench/experiments/plan", jsonReply(200, { slots: 4, errors: [], warnings: [] }));
		const c = new Client(base, { token: "tok" });
		assert.equal((await c.plan({ name: "x" })).slots, 4);
		assert.equal(requests.at(-1)?.headers["content-type"], "application/json");
		assert.deepEqual(JSON.parse(requests.at(-1)?.body ?? ""), { name: "x" });
		await c.plan("name: x\n");
		assert.equal(requests.at(-1)?.headers["content-type"], "application/yaml");
		assert.equal(requests.at(-1)?.body, "name: x\n");
	});

	it("never retries a write", async () => {
		on("POST", "/api/bench/experiments/e1/start", errorReply(502, "unavailable", "bad gateway"), jsonReply(202, statusPayload("running")));
		await assert.rejects(new Client(base, { token: "t" }).startExperiment("e1"), (err: unknown) => err instanceof HeimdallError && err.status === 502);
		assert.equal(requests.filter((r) => r.method === "POST").length, 1);
	});

	it("retries a read on 502 but not on 503", async () => {
		on("GET", "/api/bench/experiments/e1", errorReply(502, "unavailable", "bad gateway"), jsonReply(200, statusPayload("done")));
		assert.equal((await new Client(base).experiment("e1")).state, "done");
		assert.equal(requests.length, 2);
		requests = [];
		on("GET", "/api/bench/doctor", errorReply(503, "unavailable", "no doctor"), jsonReply(200, { ok: true, checks: [] }));
		await assert.rejects(new Client(base).doctor(), (err: unknown) => err instanceof HeimdallError && err.type === "unavailable");
		assert.equal(requests.length, 1);
	});

	it("refuses non-live answers when requireLive is set", async () => {
		on("GET", "/api/bench/experiments", jsonReply(200, { experiments: [] }, "recorded"));
		const strict = new Client(base, { requireLive: true });
		await assert.rejects(strict.experiments(), (err: unknown) => err instanceof ModeError && err.mode === "recorded");
		on("GET", "/api/bench/experiments", jsonReply(200, { experiments: [] }, "live"));
		assert.equal((await strict.experiments()).mode, "live");
	});

	it("waits for a terminal state", async () => {
		on("GET", "/api/bench/experiments/e1", ...["running", "analyzing", "done"].map((s) => jsonReply(200, statusPayload(s))));
		const seen: string[] = [];
		const final = await new Client(base).waitForExperiment("e1", { timeoutMs: 5000, intervalMs: 5, onStatus: (s) => seen.push(s.state) });
		assert.equal(final.state, "done");
		assert.deepEqual(seen, ["running", "analyzing", "done"]);
	});

	it("times out a wait", async () => {
		on("GET", "/api/bench/experiments/e1", jsonReply(200, statusPayload("running")));
		await assert.rejects(new Client(base).waitForExperiment("e1", { timeoutMs: 150, intervalMs: 30 }), HeimdallTimeout);
	});

	for (const [policy, blocked] of [["block", true], ["warn", false], [undefined, false]] as const) {
		it(`checks the release gate (policy ${policy ?? "none"})`, async () => {
			on("GET", "/api/bench/experiments/e1/verdict", jsonReply(200, verdictPayload(policy), "synthetic"));
			const result = await new Client(base).check("e1");
			const [decision, action, policyResult] = result;
			assert.deepEqual([decision, action], ["regressed", "hold"]);
			assert.equal(result.policyDecision, policy ?? null);
			assert.equal(result.blocked, blocked);
			assert.equal(policyResult === null, policy === undefined);
			assert.equal(result.mode, "synthetic");
			assert.equal(result.live, false);
		});
	}

	it("reads raw answers with the mode header", async () => {
		routes.set("GET /api/bench/runs/r1/diff?format=text", [() => ({ status: 200, headers: { "Content-Type": "text/plain", "X-Bench-Mode": "recorded" }, body: "diff --git a b\n" })]);
		const raw = await new Client(base).runPatch("r1");
		assert.equal(raw.mode, "recorded");
		assert.equal(raw.text(), "diff --git a b\n");
	});

	it("derives the run list from the grid", async () => {
		on("GET", "/api/bench/experiments/e1/grid", jsonReply(200, {
			exp_id: "e1", arms: ["A", "B"], conditions: ["clean"], trials: 1,
			rows: [{ task: "t1", condition: "clean", cells: [{ slot_id: "s1", run_id: "r1" }, { slot_id: "s2", run_id: null }] }],
		}));
		assert.deepEqual((await new Client(base).runs("e1")).map((c) => c.run_id), ["r1"]);
	});

	it("quotes path segments and drops empty query values", async () => {
		on("GET", "/api/bench/suites/a%2Fb/tasks/t%201", jsonReply(200, {}));
		await new Client(base).suiteTask("a/b", "t 1");
		on("GET", "/api/bench/leaderboard?suite=s&k=3", jsonReply(200, { entries: [] }));
		await new Client(base).leaderboard({ suite: "s", k: 3, condition: undefined });
		assert.equal(requests.at(-1)?.url, "/api/bench/leaderboard?suite=s&k=3");
	});

	it("times out a slow request", async () => {
		routes.set("GET /api/bench/health", [async () => {
			await new Promise((r) => setTimeout(r, 1000));
			return jsonReply(200, { status: "ok" });
		}]);
		await assert.rejects(new Client(base, { timeoutMs: 100, readRetries: 0 }).health(), HeimdallTimeout);
	});

	it("times out a body that stalls after the headers (QC2-20-04)", async () => {
		// Headers arrive at once; the body waits 300 ms unless the request is aborted.
		const stalled: typeof fetch = async (_url, init) => {
			const signal = init?.signal as AbortSignal;
			const body = new ReadableStream<Uint8Array>({
				start(controller) {
					const t = setTimeout(() => {
						controller.enqueue(new TextEncoder().encode(JSON.stringify({ mode: "live", status: "ok" })));
						controller.close();
					}, 300);
					signal.addEventListener("abort", () => {
						clearTimeout(t);
						controller.error(new DOMException("aborted", "AbortError"));
					});
				},
			});
			return new Response(body, { status: 200, headers: { "Content-Type": "application/json", "X-Bench-Mode": "live" } });
		};
		const started = Date.now();
		await assert.rejects(new Client(base, { timeoutMs: 30, readRetries: 0, fetch: stalled }).health(), HeimdallTimeout);
		assert.ok(Date.now() - started < 250, "the timeout covers the body");
	});

	it("never accepts a terminal answer after the wait deadline (QC2-20-07)", async () => {
		routes.set("GET /api/bench/experiments/e1", [async () => {
			await new Promise((r) => setTimeout(r, 300));
			return jsonReply(200, statusPayload("done"));
		}]);
		const started = Date.now();
		await assert.rejects(new Client(base, { readRetries: 0 }).waitForExperiment("e1", { timeoutMs: 50, intervalMs: 5 }), HeimdallTimeout);
		assert.ok(Date.now() - started < 250, "the status request is limited to the time left");
	});

	it("reports an unreachable server", async () => {
		await assert.rejects(new Client("http://127.0.0.1:9", { readRetries: 0, timeoutMs: 2000 }).health(),
			(err: unknown) => err instanceof HeimdallError && err.status === 0 && err.type === "connection");
	});
});
