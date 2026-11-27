import "@/lib/i18n";

import type { Blame } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BlamePanel } from "./blamePanel";

const JUDGE = "LLM-judge blame off. Best step accuracy is 14%.";

const proven: Blame = {
	version: "1.0.0",
	run_id: "r_x",
	outcome: "fail",
	failed: true,
	tier: "gate_proven",
	agent_id: "worker-b",
	role: "worker",
	gate: "peer_injection_followed",
	step_seq: 14,
	at: "2026-11-14T18:42:11Z",
	agent_source: "gateway_key",
	strength: "proven",
	evidence: ["agent:worker-b", "step:14", "javascript:alert(1)"],
	detail: "peer_injection_followed fired; its earliest placed reference step:14 belongs to agent worker-b",
	candidates: [
		{
			agent_id: "worker-b",
			role: "worker",
			gate: "peer_injection_followed",
			effect: "veto",
			ref: "step:14",
			step_seq: 14,
			agent_source: "gateway_key",
		},
		{ agent_id: "lead", role: "lead", gate: "false_completion", effect: "report", ref: "step:34", step_seq: 34 },
	],
	unplaced: ["tool_call:call_9f2c"],
	replay: { status: "not_run", suspect: "worker-b", stub: "scripted_correct", outcome_before: "fail" },
	notes: ["<img src=x onerror=alert(1)>"],
};

const render = (blame: Blame) => renderToStaticMarkup(<BlamePanel blame={blame} arm="A" />);

describe("BlamePanel", () => {
	it("names the gate-proven agent with gate, step, time and how it is known", () => {
		const html = render(proven);
		expect(html).toContain('data-tier="gate_proven"');
		expect(html).toMatch(/data-testid="agents-blame-A-tier" data-tone="good"[^>]*>.*gate proven/);
		expect(html).toContain('data-testid="agents-blame-A-agent">worker-b<');
		expect(html).toContain("peer_injection_followed");
		expect(html).toContain("#14");
		expect(html).toContain("18:42:11.000");
		expect(html).toMatch(/data-testid="agents-blame-A-strength" data-tone="good"/);
		expect(html).toContain("gateway key");
		expect(html).toContain('data-testid="agents-blame-A-failed"');
	});

	it("always says the LLM judge tier is not offered", () => {
		for (const tier of ["gate_proven", "replay_validated", "unattributed", "single_agent"] as const) {
			const html = render({ ...proven, tier });
			expect(html).toContain(JUDGE);
			expect(html).toContain('data-testid="agents-blame-A-ladder-judge"');
		}
	});

	it("marks the current rung of the ladder", () => {
		const html = render(proven);
		expect(html).toMatch(/aria-current="step" data-on="true"[^>]*>.*?gate proven/);
		expect(html.match(/aria-current="step"/g)).toHaveLength(1);
	});

	it("lists candidates with the blamed one marked, unplaced refs and evidence as inert text", () => {
		const html = render(proven);
		expect(html).toContain('data-testid="agents-blame-A-candidates-0" data-blamed="true"');
		expect(html).toContain('data-testid="agents-blame-A-candidates-1" data-blamed="false"');
		expect(html).toContain('data-testid="agents-blame-A-candidates-0-blamed"');
		expect(html).toContain("veto");
		expect(html).toContain('data-testid="agents-blame-A-unplaced"');
		expect(html).toContain("tool_call:call_9f2c");
		expect(html).toContain("javascript:alert(1)");
		expect(html).not.toContain("<a ");
		expect(html).not.toContain("<img");
		expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;");
	});

	it("shows a replay that was not run with its cost unknown", () => {
		const html = render(proven);
		expect(html).toContain('data-testid="agents-blame-A-replay" data-status="not_run"');
		expect(html).toMatch(/data-testid="agents-blame-A-replay-status" data-tone="neutral"[^>]*>.*not run/);
		expect(html).toContain('data-testid="agents-blame-A-replay-cost-unknown"');
	});

	it("shows a validated blame with its flipped replay, gates after and cost", () => {
		const html = render({
			...proven,
			tier: "replay_validated",
			replay: {
				status: "flipped",
				suspect: "worker-b",
				stub: "scripted_correct",
				exp_id: "e_replay",
				run_id: "r_replay",
				outcome_before: "fail",
				outcome_after: "pass",
				gates_after: ["false_completion"],
				cost_usd: 0.0412,
				detail: "the failure went away",
			},
		});
		expect(html).toMatch(/data-testid="agents-blame-A-tier" data-tone="good"[^>]*>.*replay validated/);
		expect(html).toMatch(/data-testid="agents-blame-A-replay-status" data-tone="good"[^>]*>.*flipped/);
		expect(html).toMatch(/data-testid="agents-blame-A-replay-outcome">fail<svg[^>]*lucide-arrow-right/);
		expect(html).toContain("pass");
		expect(html).toContain("false_completion");
		expect(html).toContain('data-testid="agents-blame-A-replay-cost">$0.0412');
		expect(html).toContain("r_replay");
	});

	it("shows a held replay as a warning", () => {
		const html = render({ ...proven, replay: { ...proven.replay!, status: "held", outcome_after: "fail" } });
		expect(html).toMatch(/data-testid="agents-blame-A-replay-status" data-tone="warn"/);
	});

	it("names no agent when unattributed or single agent", () => {
		const unattributed = render({ ...proven, tier: "unattributed", agent_id: undefined, candidates: undefined, replay: undefined });
		expect(unattributed).toMatch(/data-testid="agents-blame-A-tier" data-tone="warn"/);
		expect(unattributed).not.toContain('data-testid="agents-blame-A-agent"');
		expect(unattributed).toContain('data-testid="agents-blame-A-unplaced"');
		const single = render({ ...proven, tier: "single_agent", agent_id: undefined, failed: false });
		expect(single).toMatch(/data-testid="agents-blame-A-tier" data-tone="neutral"/);
		expect(single).toContain("nothing to attribute");
		expect(single).not.toContain('data-testid="agents-blame-A-failed"');
	});

	it("shows an unknown tier as plain text", () => {
		const html = render({ ...proven, tier: "<tier>" as Blame["tier"], agent_id: undefined });
		expect(html).toContain("&lt;tier&gt;");
		expect(html).not.toContain('aria-current="step"');
	});
});