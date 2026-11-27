import "@/lib/i18n";

import { experimentSupportPath } from "@/lib/store/apis/supportApi";
import type { SupportArm, SupportSummary } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { formatRate, formatUSD, SupportScorecard } from "./supportScorecard";

// Shaped after the $0 smoke of examples/acme-helpdesk (docs/customer-support.md): arm A the
// reference script resolves every chat, arm B does nothing.
const summary: SupportSummary = {
	exp_id: "e_hd_w3_helpdesk_smoke",
	suite: "acme-helpdesk@1.0.0",
	condition: "clean",
	support: true,
	simulator_mode: "scripted",
	simulator_label: "scripted user",
	synthetic: true,
	arms: [
		{
			arm: "A",
			label: "oracle (reference script)",
			runs: 6,
			resolution: 1,
			resolved: 6,
			decided: 6,
			cost_per_resolution: 0,
			escalation_rate: 1 / 6,
			escalation_correct: 1,
			policy_violation_rate: 0,
			user_turns: 2.5,
			simulator_cost_usd: null,
		},
		{
			arm: "B",
			label: "noop",
			runs: 6,
			resolution: 0,
			resolved: 0,
			decided: 6,
			cost_per_resolution: null,
			escalation_rate: 0,
			escalation_correct: 5 / 6,
			policy_violation_rate: 0,
			user_turns: 0,
			simulator_cost_usd: null,
		},
	],
};

describe("SupportScorecard", () => {
	it("names verified success resolution and shows the four rate cards per arm", () => {
		const html = renderToStaticMarkup(<SupportScorecard summary={summary} />);
		expect(html).toContain('data-testid="support-scorecard"');
		expect(html).toContain("Resolution");
		expect(html).toContain("Cost per resolution");
		expect(html).toContain("Escalation rate");
		expect(html).toContain("Policy violation rate");
		expect(html).toContain("6 of 6 chats resolved");
		expect(html).toContain("smoke test, not findings");
		expect(html).toContain('data-testid="support-A-resolution"');
		expect(html).toContain('data-testid="support-B-cost"');
	});

	it("labels a model-played user and warns against mixing", () => {
		const llm: SupportSummary = { ...summary, simulator_mode: "llm", simulator_label: "simulated user (model: anthropic/claude-sim)", synthetic: false };
		const html = renderToStaticMarkup(<SupportScorecard summary={llm} />);
		expect(html).toContain("simulated user (model: anthropic/claude-sim)");
		expect(html).toContain("Compare only under the same simulator");
		expect(html).not.toContain("smoke test");
	});

	it("formats unknowns as unknown", () => {
		expect(formatRate(null)).toBe("unknown");
		expect(formatRate(0.5)).toBe("50%");
		expect(formatUSD(null)).toBe("unknown");
		expect(formatUSD(0.0123)).toBe("$0.0123");
		expect(formatUSD(12.5)).toBe("$12.50");
	});

	it("escapes the experiment id in the request path", () => {
		expect(experimentSupportPath("e/x")).toBe("/experiments/e%2Fx/support");
	});
});

// The Go side's key lists (bench/handlers/testdata/contract/<Type>.keys): the fixture above carries
// exactly the listed keys, required and optional.
const CONTRACT_DIR = path.resolve(__dirname, "../../../handlers/testdata/contract");

function keyList(typ: string): { required: string[]; optional: string[] } {
	const lines = fs
		.readFileSync(path.join(CONTRACT_DIR, `${typ}.keys`), "utf8")
		.split("\n")
		.map((l) => l.trim())
		.filter((l) => l && !l.startsWith("#"));
	return { required: lines.filter((l) => !l.endsWith("?")), optional: lines.filter((l) => l.endsWith("?")).map((l) => l.slice(0, -1)) };
}

function checkObject(obj: object, typ: string) {
	const { required, optional } = keyList(typ);
	const keys = Object.keys(obj);
	for (const k of required) expect(keys, `${typ} needs ${k}`).toContain(k);
	for (const k of keys) expect([...required, ...optional], `${typ} has no key ${k}`).toContain(k);
}

describe("support contract", () => {
	it("matches the Go key lists", () => {
		checkObject(summary, "SupportSummary");
		summary.arms.forEach((a: SupportArm) => checkObject(a, "SupportArm"));
	});
});
