import { describe, expect, it } from "vitest";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { suggestedRaise } from "./raiseBudget";

const ev = (children: unknown[]) => ({ id: "ev_1", status: "done", spec: {}, children }) as unknown as Evaluation;

describe("suggestedRaise (N2)", () => {
	it("prefills from what is left, not two dollars", () => {
		// 10 runs left at about $0.003 each: 0.05 + 10 * 0.003 * 1.25 = 0.0875, up to the cent.
		const e = ev([{ experiment_id: "e1", status: "done", budget_usd: 0.05, spent_usd: 0.05, budget_stopped: true, not_started: 10, next_run_usd: 0.003 }]);
		expect(suggestedRaise(e)).toBe(0.09);
	});
	it("adds the benchmark's own cap once without an estimate", () => {
		const e = ev([{ experiment_id: "e1", status: "done", budget_usd: 3, spent_usd: 3, budget_stopped: true, not_started: 4 }]);
		expect(suggestedRaise(e)).toBe(6);
	});
});
