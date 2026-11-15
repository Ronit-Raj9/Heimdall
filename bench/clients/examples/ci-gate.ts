// CI release gate over the Heimdall bench API, TypeScript edition.
//
// Plan an experiment, run it, wait for it, and fail the job when the release
// policy blocks. Or, with --exp, gate an experiment that already ran.
//
//   node --experimental-strip-types ci-gate.ts experiments/smoke.yaml
//   node --experimental-strip-types ci-gate.ts --exp e_pilot_mixed_nc93 --allow-recorded
//
// Environment: HEIMDALL_URL (default http://127.0.0.1:8090) and BENCH_WRITE_TOKEN
// (needed to plan and start; reads need no token).
//
// Exit codes: 0 gate passed, 1 policy blocks, 2 experiment did not finish,
// 3 server is not live (unless --allow-recorded), 4 plan refused or request failed.
import { readFileSync } from "node:fs";
import { parseArgs } from "node:util";
import { Client, HeimdallError, HeimdallTimeout, ModeError } from "../typescript/src/index.ts";

const { values, positionals } = parseArgs({
	allowPositionals: true,
	options: {
		exp: { type: "string" },
		url: { type: "string", default: process.env.HEIMDALL_URL ?? "http://127.0.0.1:8090" },
		token: { type: "string", default: process.env.BENCH_WRITE_TOKEN ?? "" },
		timeout: { type: "string", default: "3600" },
		interval: { type: "string", default: "10" },
		"allow-recorded": { type: "boolean", default: false },
		"fail-on-warn": { type: "boolean", default: false },
	},
});

async function main(): Promise<number> {
	const file = positionals[0];
	if (Boolean(file) === Boolean(values.exp)) {
		console.error("usage: ci-gate.ts <experiment.yaml> | --exp <id> [--allow-recorded]");
		return 4;
	}
	const client = new Client(values.url!, { token: values.token || undefined, requireLive: !values["allow-recorded"] });
	try {
		const health = await client.health();
		console.log(`server ${values.url}, mode ${health.mode}`);
		const expId = values.exp ?? (await run(client, file!));
		if (!expId) return 4;
		const status = await client.experiment(expId);
		if (status.state !== "done") {
			console.log(`experiment ${expId} ended ${status.state}: no release decision`);
			return 2;
		}
		const result = await client.check(expId);
		console.log(`verdict ${result.decision}, action ${result.action}, policy ${result.policyDecision ?? "none"} (mode ${result.mode})`);
		for (const c of result.policyResult?.checks ?? []) {
			if (c.status !== "pass") console.log(`  ${c.status} ${c.id}: ${c.threshold} ${c.reason ?? ""}`);
		}
		if (result.blocked || (values["fail-on-warn"] && result.policyDecision === "warn")) {
			console.log(`gate FAILED: release policy ${result.policyDecision}`);
			return 1;
		}
		console.log("gate passed");
		return 0;
	} catch (err) {
		if (err instanceof ModeError) {
			console.log(`refusing to gate: ${err.serverMessage} (pass --allow-recorded for a demo)`);
			return 3;
		}
		if (err instanceof HeimdallTimeout) {
			console.log(`timed out: ${err.message}`);
			return 2;
		}
		if (err instanceof HeimdallError) {
			console.log(`request failed: ${err.message}`);
			return 4;
		}
		throw err;
	}
}

/** Plan, create, start and wait; resolves to the experiment id, or "" when the plan has errors. */
async function run(client: Client, path: string): Promise<string> {
	const text = readFileSync(path, "utf8");
	const plan = await client.plan(text);
	console.log(`plan: ${plan.slots} slots, ${plan.pairs} pairs, MDE ${plan.mde.toFixed(1)} pp`);
	for (const w of plan.warnings) console.log(`  warning: ${w}`);
	if (plan.errors.length > 0) {
		for (const e of plan.errors) console.log(`  error: ${e}`);
		return "";
	}
	const started = await client.runExperiment(text);
	console.log(`started ${started.exp_id}`);
	await client.waitForExperiment(started.exp_id, {
		timeoutMs: Number(values.timeout) * 1000,
		intervalMs: Number(values.interval) * 1000,
		onStatus: (s) => console.log(`  ${s.state}: ${s.slots.done}/${s.slots.total} slots done, ${s.slots.running} running, ${s.slots.failed} failed`),
	});
	return started.exp_id;
}

process.exitCode = await main();
