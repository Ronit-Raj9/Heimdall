// Unit tests of V1Client against a local fake server (node:test, node:http).
// Run: node --experimental-strip-types --test test/v1.test.ts
import assert from "node:assert/strict";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { after, before, beforeEach, describe, it } from "node:test";
import { HeimdallError, HeimdallTimeout, ModeError, V1_ROUTES, V1Client, armScore, normalizeReportCard } from "../src/index.ts";

type Reply = { status: number; headers: Record<string, string>; body: string };
type Recorded = { method: string; url: string; headers: IncomingMessage["headers"]; body: string };

const jsonReply = (status: number, payload: Record<string, unknown>, mode = "live"): Reply => ({
	status,
	headers: { "Content-Type": "application/json; charset=utf-8", "X-Bench-Mode": mode },
	body: JSON.stringify({ mode, ...payload }),
});

let server: Server;
let base = "";
let routes = new Map<string, Reply[]>();
let requests: Recorded[] = [];
const on = (method: string, url: string, ...replies: Reply[]) => routes.set(`${method} ${url}`, replies);

before(async () => {
	server = createServer((req: IncomingMessage, res: ServerResponse) => {
		const chunks: Buffer[] = [];
		req.on("data", (c: Buffer) => chunks.push(c));
		req.on("end", () => {
			requests.push({ method: req.method ?? "", url: req.url ?? "", headers: req.headers, body: Buffer.concat(chunks).toString() });
			const q = routes.get(`${req.method} ${req.url}`);
			const reply = q && q.length > 0 ? (q.length > 1 ? q.shift()! : q[0]!) : jsonReply(404, { error: { message: `route not found: ${req.url}`, type: "not_found" } });
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

const REPORT = {
	evaluation: "ev_1",
	status: "done",
	arms: [{ label: "A", agent: "bot", version: 2, model: "m1" }],
	capabilities: [{ capability: "coding", benchmarks: ["c1"], arms: { A: { score: 0.5, ci_low: 0.2, ci_high: 0.8, runs: 4 } } }],
	benchmarks: [
		{ benchmark: "c1", capability: "coding", experiment_id: "e_1", arms: { A: { score: 0.5, ci_low: 0.2, ci_high: 0.8, runs: 4 } } },
		{ benchmark: "s1", capability: "support", experiment_id: "e_2", arms: { A: { score: 1, ci_low: 0.5, ci_high: 1, runs: 12 } } },
	],
	reliability: [],
	safety: { attack_resisted: { A: 0.9 }, cheat_rate: {} },
};
const SPEC = { name: "nightly", mode: "score" as const, arms: [{ label: "A", agent: "bot", model: "m1" }], benchmarks: ["c1"] };

describe("V1Client", () => {
	it("calls /api/v1 paths with the token", async () => {
		on("GET", "/api/v1/agents/my%20bot/compatibility", jsonReply(200, { agent: "my bot", benchmarks: [{ id: "c1", ok: true }] }));
		on("GET", "/api/v1/runs?exp=e_1&limit=5", jsonReply(200, { runs: [], total: 0 }));
		on("GET", "/api/v1/leaderboard/agents?capability=coding", jsonReply(200, { rows: [] }));
		on("POST", "/api/v1/agents/bot/versions", jsonReply(201, { version: 3, created: true }));
		const hd = new V1Client(base, { token: "hm_tok" });
		assert.equal((await hd.compatibility("my bot")).benchmarks[0]?.ok, true);
		await hd.runs({ exp: "e_1", limit: 5, suite: undefined });
		await hd.leaderboard({ capability: "coding" });
		assert.equal((await hd.createAgentVersion("bot", "ci")).version, 3);
		for (const r of requests) assert.equal(r.headers.authorization, "Bearer hm_tok");
		assert.deepEqual(JSON.parse(requests.at(-1)!.body), { note: "ci" });
	});

	it("normalizes report cards and scores arms", async () => {
		on("GET", "/api/v1/evaluations/ev_1/report", jsonReply(200, REPORT));
		const rc = await new V1Client(base).report("ev_1");
		assert.equal(rc.mode, "live");
		assert.equal(rc.benchmarks[0]?.arms.A?.cost_usd, 0);
		assert.equal(armScore(rc, "A"), (0.5 * 4 + 12) / 16);
		assert.equal(armScore(rc, "Z"), null);
		const empty = normalizeReportCard({ evaluation: "x" });
		assert.deepEqual(empty.benchmarks, []);
		assert.deepEqual(empty.safety, { attack_resisted: {}, cheat_rate: {} });
	});

	it("reads an agent report card, or none", async () => {
		on("GET", "/api/v1/agents/bot/report-card?version=2", jsonReply(200, { agent: "bot", version: 2, report: REPORT }));
		on("GET", "/api/v1/agents/new/report-card", jsonReply(200, { agent: "new", report: null }));
		const hd = new V1Client(base);
		assert.equal((await hd.agentReportCard("bot", { version: 2 })).report?.evaluation, "ev_1");
		assert.equal((await hd.agentReportCard("new")).report, null);
	});

	it("evaluates and waits for the report card", async () => {
		on("POST", "/api/v1/evaluations", jsonReply(202, { id: "ev_1", status: "queued", children: [] }));
		on("GET", "/api/v1/evaluations/ev_1", jsonReply(200, { id: "ev_1", status: "running" }), jsonReply(200, { id: "ev_1", status: "done" }));
		on("GET", "/api/v1/evaluations/ev_1/report", jsonReply(200, REPORT));
		const seen: string[] = [];
		const result = await new V1Client(base).evaluateAndWait(SPEC, { intervalMs: 5, onStatus: (ev) => seen.push(ev.status) });
		assert.equal(result.succeeded, true);
		assert.deepEqual(seen, ["running", "done"]);
		assert.equal(result.report.evaluation, "ev_1");
		assert.equal(JSON.parse(requests[0]!.body).name, "nightly");
	});

	it("cancels the evaluation on timeout", async () => {
		on("POST", "/api/v1/evaluations", jsonReply(202, { id: "ev_9", status: "queued" }));
		on("GET", "/api/v1/evaluations/ev_9", jsonReply(200, { id: "ev_9", status: "running" }));
		on("POST", "/api/v1/evaluations/ev_9/cancel", jsonReply(202, { id: "ev_9", status: "cancelled" }));
		await assert.rejects(new V1Client(base).evaluateAndWait(SPEC, { timeoutMs: 150, intervalMs: 30 }), HeimdallTimeout);
		assert.ok(requests.some((r) => r.method === "POST" && r.url === "/api/v1/evaluations/ev_9/cancel"));
	});

	it("surfaces a scope refusal and refuses non-live answers when asked", async () => {
		on("POST", "/api/v1/evaluations", jsonReply(403, { error: { message: "scope evaluate required", type: "forbidden" } }));
		await assert.rejects(new V1Client(base).createEvaluation(SPEC), (err: unknown) => err instanceof HeimdallError && err.status === 403);
		on("GET", "/api/v1/evaluations", jsonReply(200, { evaluations: [] }, "recorded"));
		await assert.rejects(new V1Client(base, { requireLive: true }).evaluations(), ModeError);
	});

	it("pushes, tests and reads agents as code", async () => {
		const doc = "id: support-bot  # the agent\nadapter: remote_http\n";
		const errReply = (status: number, type: string) => jsonReply(status, { error: { message: type, type } });
		on("POST", "/api/v1/agents", jsonReply(201, { id: "support-bot", path: "agents/support-bot.yaml" }), errReply(409, "conflict"));
		on("PUT", "/api/v1/agents/support-bot", jsonReply(200, { id: "support-bot", sha256: "abc" }));
		on("POST", "/api/v1/agents/support-bot/versions", jsonReply(201, { version: 1, created: true }), jsonReply(200, { version: 2, created: true }));
		on("POST", "/api/v1/agents/support-bot/test", jsonReply(200, { ok: true, answer: "4", trace: [], notes: [] }));
		on("GET", "/api/v1/agents/support-bot", jsonReply(200, { id: "support-bot", yaml: doc, sha256: "abc" }));
		const hd = new V1Client(base, { token: "hm_tok" });
		const first = await hd.pushAgent(doc, { note: "ci" });
		assert.deepEqual([first.created, first.version.version], [true, 1]);
		const second = await hd.pushAgent(doc);
		assert.deepEqual([second.created, second.version.version], [false, 2]);
		assert.deepEqual([requests[0]!.method, requests[0]!.url, JSON.parse(requests[0]!.body)], ["POST", "/api/v1/agents", { yaml: doc, id: "support-bot" }]);
		assert.deepEqual([requests[3]!.method, JSON.parse(requests[3]!.body)], ["PUT", { yaml: doc }]);
		assert.equal((await hd.testAgent("support-bot")).ok, true);
		assert.equal((await hd.getAgent("support-bot")).sha256, "abc");
		on("POST", "/api/v1/agents", jsonReply(200, { id: "x", dry_run: true }));
		await hd.createAgent({ template: "http-url", id: "x", params: { url: "https://a.example" }, dryRun: true });
		assert.deepEqual(JSON.parse(requests.at(-1)!.body), { template: "http-url", id: "x", params: { url: "https://a.example" }, dry_run: true });
		await assert.rejects(hd.pushAgent("adapter: remote_http\n"), /no top-level id/);
		on("POST", "/api/v1/agents", errReply(400, "bad_request"));
		await assert.rejects(hd.pushAgent(doc), (err: unknown) => err instanceof HeimdallError && err.status === 400);
	});

	it("exports traces raw", async () => {
		on("GET", "/api/v1/runs/r_1/trace?format=jsonl", { status: 200, headers: { "Content-Type": "application/x-ndjson", "X-Bench-Mode": "live" }, body: '{"step":1}\n' });
		on("GET", "/api/v1/runs/r_1/otlp?payloads=true", { status: 200, headers: { "Content-Type": "application/json", "X-Bench-Mode": "live" }, body: "{}" });
		const hd = new V1Client(base);
		const t = await hd.trace("r_1", "jsonl");
		assert.equal(t.mode, "live");
		assert.match(t.text(), /step/);
		assert.equal((await hd.otlp("r_1", true)).text(), "{}");
		for (const [, path] of V1_ROUTES) assert.ok(path.startsWith("/api/v1/"));
	});

	it("manages a benchmark as code", async () => {
		on("POST", "/api/v1/benchmarks", jsonReply(201, { id: "faq", files: ["suite.yaml"] }));
		on("PUT", "/api/v1/benchmarks/faq/grader", jsonReply(200, { suite: "faq", grader: "answer_match" }));
		on("POST", "/api/v1/benchmarks/faq/tasks", jsonReply(201, { benchmark: "faq", version: { version: 1, added: ["q1"] }, added: ["q1"], problems: [] }));
		on("GET", "/api/v1/benchmarks/faq/tasks?limit=50", jsonReply(200, { benchmark: "faq", version: 1, total: 1, offset: 0, tasks: [{ id: "q1", from_rows: true }] }));
		on("GET", "/api/v1/benchmarks/faq/versions", jsonReply(200, { benchmark: "faq", versions: [{ version: 1 }] }));
		on("GET", "/api/v1/benchmarks/faq/versions/1", jsonReply(200, { benchmark: "faq", version: { version: 1 }, tasks: { q1: "ab" } }));
		on("DELETE", "/api/v1/benchmarks/faq/tasks/q1", jsonReply(200, { benchmark: "faq", task: "q1", version: { version: 2 } }));
		on("PUT", "/api/v1/benchmarks/faq/data-policy", jsonReply(200, { benchmark: "faq", policy: { pii: { mode: "mask" }, access: { mode: "restricted" } } }));
		on("POST", "/api/v1/imports/platforms", jsonReply(200, { platform: "langfuse", read: 2, preview: [] }));
		const hd = new V1Client(base, { token: "hm_tok" });
		assert.equal((await hd.createBenchmark("faq")).id, "faq");
		assert.deepEqual(JSON.parse(requests.at(-1)!.body), { id: "faq", template: "qa", blank: true });
		await hd.setGrader("faq", "answer_match");
		assert.deepEqual(JSON.parse(requests.at(-1)!.body), { grader: "answer_match" });
		const res = await hd.upsertTasks("faq", [{ id: "q1", input: "2+2?", expected: ["4"], metadata: { tier: "free" } }], { source: "ci" });
		assert.deepEqual(res.added, ["q1"]);
		assert.equal(JSON.parse(requests.at(-1)!.body).source, "ci");
		assert.equal((await hd.datasetTasks("faq", { limit: 50 })).tasks?.[0]?.id, "q1");
		assert.equal((await hd.datasetVersions("faq")).versions?.[0]?.version, 1);
		assert.equal((await hd.datasetVersion("faq", 1)).tasks.q1, "ab");
		assert.equal((await hd.deleteTask("faq", "q1")).version?.version, 2);
		assert.equal(requests.at(-1)!.method, "DELETE");
		await hd.setDataPolicy("faq", { pii: { mode: "mask" }, access: { mode: "restricted", users: ["u_1"] } });
		assert.equal(requests.at(-1)!.method, "PUT");
		assert.equal((await hd.importFromPlatform({ platform: "langfuse", kind: "dataset", dataset: "d", target: "benchmark", benchmark: "faq", dry_run: true })).read, 2);
		for (const r of requests) assert.equal(r.headers.authorization, "Bearer hm_tok");
	});
});
