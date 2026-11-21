// Smoke test of the client against a real bench server. Skipped unless
// HEIMDALL_URL is set. bench/clients/contract_test.go starts the real server in
// recorded mode over a copy of bench/testdata/recorded and runs this file with
// HEIMDALL_URL, HEIMDALL_TOKEN and HEIMDALL_EXPECT_MODE, so the client cannot
// drift from the server silently. Read routes only, plus write routes that a
// recorded server refuses (no launcher), so nothing changes.
import assert from "node:assert/strict";
import { before, describe, it } from "node:test";
import { Client, HeimdallError, ROUTES, V1Client, V1_ROUTES, modeOf } from "../src/index.ts";

const url = process.env.HEIMDALL_URL ?? "";
const token = process.env.HEIMDALL_TOKEN || undefined;
const DECISIONS = ["improved", "regressed", "inconclusive", "invalid"];
const ACTIONS = ["ship", "hold", "no_decision", "fix_harness"];

describe("live server", { skip: url ? false : "HEIMDALL_URL not set" }, () => {
	const client = new Client(url || "http://127.0.0.1:9", { token, timeoutMs: 60_000 });
	let mode = process.env.HEIMDALL_EXPECT_MODE ?? "";

	before(async () => {
		if (!mode) mode = (await client.health()).mode;
	});

	it("is described by openapi.json, route by route", async () => {
		const doc = await client.openapi();
		assert.equal(doc.openapi, "3.1.0");
		assert.equal(doc["x-bench-mode"], mode);
		for (const [method, path] of ROUTES) {
			assert.ok(doc.paths[path]?.[method.toLowerCase()], `client calls ${method} ${path}, which the server does not describe`);
		}
	});

	it("describes every v1 client route in /api/v1/openapi.json", async () => {
		const hd = new V1Client(url || "http://127.0.0.1:9", { token, timeoutMs: 60_000 });
		const doc = await hd.openapi();
		assert.match(doc.openapi, /^3\./);
		for (const [method, path] of V1_ROUTES) {
			assert.ok(doc.paths[path]?.[method.toLowerCase()], `v1 client calls ${method} ${path}, which the server does not describe`);
		}
		assert.equal((await hd.agents()).mode, mode);
		assert.equal((await hd.evaluations()).mode, mode);
	});

	it("reads health, catalog, suites, agents, imports, workers", async () => {
		assert.equal((await client.health()).status, "ok");
		assert.equal(modeOf(await client.catalog()), mode);
		const suites = await client.suites();
		assert.equal(suites.mode, mode);
		const withTasks = suites.suites.find((s) => s.tasks.length > 0);
		if (withTasks) assert.equal((await client.suiteTask(withTasks.id, withTasks.tasks[0]!.id)).mode, mode);
		assert.equal((await client.agents()).mode, mode);
		assert.equal((await client.imports()).mode, mode);
		const workers = await client.workers();
		assert.equal(workers.mode, mode);
		assert.equal(typeof workers.enabled, "boolean");
	});

	it("reads every experiment, its runs and its release decision", async () => {
		const listing = await client.experiments();
		assert.equal(listing.mode, mode);
		assert.ok(listing.experiments.length > 0, "the recorded bundle has experiments");
		for (const exp of listing.experiments) {
			const status = await client.experiment(exp.exp_id);
			assert.equal(status.mode, mode);
			assert.equal((await client.waitForExperiment(exp.exp_id, { timeoutMs: 5000, intervalMs: 100 })).state, status.state);
			const cells = await client.runs(exp.exp_id);
			assert.ok(cells.length > 0);
			const runId = cells[0]!.run_id!;
			const detail = await client.run(runId);
			assert.equal(detail.mode, mode);
			assert.equal(detail.run.run_id, runId);
			assert.equal((await client.runDiff(runId)).mode, mode);
			assert.equal((await client.runReceipts(runId)).mode, mode);
			assert.equal((await client.runEvents(runId)).mode, mode);
			assert.equal((await client.runPatch(runId)).mode, mode);
			assert.equal((await client.runFile(runId, "run.json")).mode, mode);
			assert.equal((await client.pair(cells[0]!.pair_id)).mode, mode);
			assert.equal((await client.events(exp.exp_id)).mode, mode);
			assert.equal((await client.analysis(exp.exp_id)).mode, mode);
			assert.equal((await client.triage(exp.exp_id)).mode, mode);
			const result = await client.check(exp.exp_id);
			assert.ok(DECISIONS.includes(result.decision), result.decision);
			assert.ok(ACTIONS.includes(result.action), result.action);
			assert.equal(result.mode, mode);
			if (result.policyResult) assert.ok(["allow", "warn", "block"].includes(result.policyResult.decision));
		}
	});

	it("reads the leaderboard and the release log", async () => {
		assert.equal((await client.leaderboard()).mode, mode);
		const releases = await client.releases();
		assert.equal(releases.mode, mode);
		const first = releases.releases[0];
		if (first) assert.equal((await client.release(first.release_id)).mode, mode);
	});

	it("carries the server's error type and message", async () => {
		await assert.rejects(client.experiment("no-such-experiment"), (err: unknown) => {
			assert.ok(err instanceof HeimdallError);
			assert.deepEqual([err.status, err.type, err.mode], [404, "not_found", mode]);
			assert.match(err.serverMessage, /not found/);
			return true;
		});
		await assert.rejects(new Client(url).plan({ name: "x" }), (err: unknown) => err instanceof HeimdallError && err.status === 401 && err.type === "unauthorized");
		if (mode !== "live" && token) {
			// A recorded server has no launcher: the token passes, the route answers 503.
			await assert.rejects(client.plan("name: x\n"), (err: unknown) => err instanceof HeimdallError && err.status === 503 && err.type === "unavailable");
		}
	});
});
