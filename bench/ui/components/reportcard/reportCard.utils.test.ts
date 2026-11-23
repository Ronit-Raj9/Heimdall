import { describe, expect, it } from "vitest";
import { faultChange } from "./reportCard.utils";

describe("faultChange (N126)", () => {
	it("is the change from clean: negative hurts, positive helped, 0 is no change", () => {
		expect(faultChange({ drop: -0.67 })).toEqual({ text: "-67 pts", hurt: true, helped: false });
		expect(faultChange({ drop: 0.04 })).toEqual({ text: "+4 pts", hurt: false, helped: true });
		expect(faultChange({ drop: 0 })).toEqual({ text: "no change", hurt: false, helped: false });
		expect(faultChange(undefined)).toEqual({ text: "-", hurt: false, helped: false });
	});
});
import type { Report } from "@/lib/store/apis/evaluationsApi";
import { capabilityRows, headline, overallScore } from "./reportCard.utils";

const empty = { score: 0, ci_low: 0, ci_high: 0, runs: 0, cost_usd: null, tokens: null, steps: 0, tool_calls: 0, wall_s: 0 };
const counted = (score: number, runs: number) => ({ ...empty, score, ci_low: score, ci_high: score, runs, cost_usd: 0.1, tokens: 10 });

/** The live N215 card: score mode, BFCL, every run thrown out by the model check. */
const thrownOut: Report = {
	evaluation: "ev_20261122_200956_49a8cb",
	status: "done",
	mode: "score",
	arms: [{ label: "A", agent: "codex", version: 1, model: "gpt-6-luna" }],
	capabilities: [{ capability: "support", benchmarks: ["hd-harbor-bfcl"], arms: { A: empty } }],
	benchmarks: [
		{
			benchmark: "hd-harbor-bfcl",
			capability: "support",
			experiment_id: "e_eval_5rcc",
			arms: { A: empty },
			thrown_out: { A: { runs: 24, checks: { model_fidelity: 24 } } },
		},
	],
	reliability: [],
	thrown_out_runs: 24,
};

describe("report card headline without runs (fix wave 3, N182, N215)", () => {
	it("has no overall score when no row has runs", () => {
		expect(overallScore(thrownOut, "A")).toBeUndefined();
	});

	it("says every run was thrown out instead of 0%", () => {
		const h = headline(thrownOut, (g) => (g === "model_fidelity" ? "the model check" : g));
		expect(h.title).toBe("No score: every run was thrown out by a safety check");
		expect(h.detail).toContain("24 runs on hd-harbor-bfcl failed the model check.");
		expect(h.detail).toContain("not with your agent");
	});

	it("keeps the score and names the thrown-out runs when some counted", () => {
		const some: Report = {
			...thrownOut,
			benchmarks: [
				{ ...thrownOut.benchmarks[0], arms: { A: counted(0.75, 12) }, thrown_out: { A: { runs: 7, checks: { model_fidelity: 7 } } } },
			],
			thrown_out_runs: 7,
		};
		const h = headline(some);
		expect(h.title).toBe("Your agent scores 75% overall");
		expect(h.detail).toContain("7 runs were thrown out by a safety check and are not in the score.");
	});

	it("says how far a running evaluation is", () => {
		const running: Report = { ...thrownOut, status: "running", thrown_out_runs: 0, planned_runs: 6, done_runs: 2 };
		expect(headline(running).title).toBe("Still running: 2 of 6 runs done");
	});
});

describe("capability rows (N185)", () => {
	it("lists tested capabilities first and none on a card without runs", () => {
		const bfcl: Report = {
			...thrownOut,
			capabilities: [{ capability: "support", benchmarks: ["hd-harbor-bfcl"], arms: { A: counted(0.5, 4) } }],
			benchmarks: [{ ...thrownOut.benchmarks[0], arms: { A: counted(0.5, 4) }, thrown_out: undefined }],
			thrown_out_runs: 0,
		};
		const rows = capabilityRows(bfcl);
		expect(rows[0]).toMatchObject({ capability: "support", tested: true });
		expect(rows.find((r) => r.capability === "terminal")).toMatchObject({ tested: false });
		expect(capabilityRows(thrownOut)).toEqual([]);
	});
});