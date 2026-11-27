import { describe, expect, it } from "vitest";
import { listCostNews, listHasRunning } from "./experimentBoard";

const stat = (arm: string, cost: number | null, runs: number, pass: number | null) => ({
	arm,
	runs,
	passed: 0,
	pass_rate: pass,
	cost_usd: cost,
	cost_runs: runs,
});

describe("listCostNews (M4-66)", () => {
	it("names the much cheaper setup per run and a same score", () => {
		expect(listCostNews(["A", "B"], [stat("A", 3, 2, 0.5), stat("B", 0.2, 2, 0.5)])).toEqual({
			arm: "B",
			ratio: 15,
			sameScore: true,
		});
	});
	it("says unclear when the pass rates differ", () => {
		expect(listCostNews(["A", "B"], [stat("A", 1, 1, 0.5), stat("B", 4, 1, 0.6)])?.sameScore).toBe(false);
	});
	it("is null with an unknown cost, a small gap or no stats", () => {
		expect(listCostNews(["A", "B"], [stat("A", null, 2, 0.5), stat("B", 1, 2, 0.5)])).toBeNull();
		expect(listCostNews(["A", "B"], [stat("A", 1, 1, 0.5), stat("B", 1.5, 1, 0.5)])).toBeNull();
		expect(listCostNews(["A", "B"], undefined)).toBeNull();
	});
});

describe("listHasRunning (M4-79)", () => {
	it("is true only while a row runs or is analyzed", () => {
		expect(listHasRunning([{ state: "done" }, { state: "analyzing" }] as never)).toBe(true);
		expect(listHasRunning([{ state: "done" }] as never)).toBe(false);
		expect(listHasRunning(undefined)).toBe(false);
	});
});

describe("setupsLine names the arm when agent and model match (Q-254)", () => {
	it("appends the arm", async () => {
		const { setupsLine } = await import("./experimentBoard");
		const s = (arm: string) => ({ arm, agent: "codex", agent_file: "a", model: "m" });
		expect(setupsLine({ setups: [s("clean"), s("tool-timeout")] })).toBe("codex m (clean) vs codex m (tool-timeout)");
	});
});