import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { PairDivergenceResponse } from "@/lib/store/apis/behaviourApi";
import type { RunDetail, RunSteps, StepRecord } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { alignSteps, StepsSideBySideView } from "./stepsSideBySide";

const step = (seq: number, kind: string, summary: string, extra: Partial<StepRecord> = {}) =>
	({ seq, ts: "2026-11-28T10:00:00Z", kind, source: "gateway", proof: "verified", turn: 1, summary, ...extra }) as StepRecord;

// Shapes of GET /runs/{id}, /runs/{id}/steps and /runs/{id}/divergence/{other} (handlers/divergence_w441.go).
const obs = (v: number) => ({ state: "observed", v });
const detail = (id: string, outcome: string, steps: number) =>
	({
		slot: { slot_id: `s_${id}`, pair_id: "p1", exp_id: "e1", arm: "A", task: "t1", condition: "clean", trial: 1 },
		run: { run_id: id, outcome },
		metrics: { steps: obs(steps), tool_calls: obs(2), tokens: obs(1200), cost_usd: obs(0.0042), wall_time_s: obs(75) },
	}) as unknown as RunDetail;
const stepsA: RunSteps = {
	run_id: "r_a",
	available: true,
	observers: [],
	steps: [
		step(1, "model_turn", "model answered: 1 tool call"),
		step(2, "tool_request", "shell: ls", { tool_name: "shell" }),
		step(3, "tool_execution", "ls exited 0"),
		step(4, "tool_request", "shell: pytest -q", { tool_name: "shell" }),
		step(5, "tool_execution", "x".repeat(600)),
	],
};
const stepsB: RunSteps = {
	run_id: "r_b",
	available: true,
	observers: [],
	steps: [step(1, "model_turn", "model answered: 1 tool call"), step(2, "tool_request", "shell: ls", { tool_name: "shell" }), step(3, "tool_execution", "ls exited 0"), step(4, "tool_request", "edit: setup.py", { tool_name: "edit" })],
};
const div = {
	pair_id: "p1",
	arms: ["A", "B"],
	runs: ["r_a", "r_b"],
	outcomes: ["pass", "fail"],
	available: true,
	divergence: { found: true, category: "tool", seq_a: 4, seq_b: 4, after: [], note: "" },
} as unknown as PairDivergenceResponse;

describe("alignSteps (K2)", () => {
	it("pairs the shared prefix, marks the divergence, then pairs the rest", () => {
		const rows = alignSteps(stepsA.steps, stepsB.steps, div.divergence);
		expect(rows.map((r) => r.kind)).toEqual(["pair", "pair", "pair", "diverge", "pair", "pair"]);
		const last = rows[5];
		expect(last.kind === "pair" && last.a?.seq === 5 && last.b === null).toBe(true);
	});
	it("pads unequal prefixes so the marker stays one row", () => {
		const rows = alignSteps(stepsA.steps, stepsB.steps.slice(0, 2), { ...div.divergence!, seq_b: 0 });
		expect(rows.filter((r) => r.kind === "diverge")).toHaveLength(1);
	});
	it("no divergence found: plain pairing, no marker", () => {
		expect(alignSteps(stepsA.steps, stepsB.steps, { ...div.divergence!, found: false }).some((r) => r.kind === "diverge")).toBe(false);
	});
});

describe("StepsSideBySideView (K2)", () => {
	it("renders both step lists from the handlers' JSON with the divergence marked and long output folded", () => {
		const html = render(
			<StepsSideBySideView labels={["A", "B"]} details={[detail("r_a", "pass", 5), detail("r_b", "fail", 4)]} steps={[stepsA, stepsB]} divergence={div} />,
		);
		expect(html).toContain('data-testid="steps-sbs-diverge"');
		expect(html.indexOf("edit: setup.py")).toBeGreaterThan(html.indexOf("steps-sbs-diverge"));
		expect(html.indexOf("shell: ls")).toBeLessThan(html.indexOf("steps-sbs-diverge"));
		expect(html).toContain('data-testid="steps-sbs-fold"');
		expect(html).toContain("Tool calls");
		expect(html).toContain("$0.0042");
		expect(html).toContain("1m 15s");
		expect(html).toContain(">Solved<");
		expect(html).toContain(">Not solved<");
		expect(html).toContain("Run ended");
	});
});
