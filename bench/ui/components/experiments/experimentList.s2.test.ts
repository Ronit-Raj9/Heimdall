import { describe, expect, it } from "vitest";
import { anyRunning, deleteMany, isTestExperiment } from "@/components/experiments/experimentList";
import type { ExperimentStatus } from "@/lib/types/bench";

const exp = (state: string) => ({ exp_id: state, state }) as unknown as ExperimentStatus;

describe("experiment list (X-76, M4-79)", () => {
	it("polls only while something is working", () => {
		expect(anyRunning([exp("done"), exp("failed")])).toBe(false);
		expect(anyRunning([exp("done"), exp("running")])).toBe(true);
	});
	it("deletes in turn and stops at the first failure", async () => {
		const seen: string[] = [];
		const res = await deleteMany(["a", "b", "c"], async (id) => {
			seen.push(id);
			if (id === "b") throw new Error("refused");
		});
		expect(res.deleted).toEqual(["a"]);
		expect(res.error).toBeInstanceOf(Error);
		expect(seen).toEqual(["a", "b"]);
	});
	it("marks test comparisons by name or id (M1-18)", () => {
		expect(isTestExperiment({ exp_id: "exp-1", name: "zz-test-compare" })).toBe(true);
		expect(isTestExperiment({ exp_id: "zz_test 9", name: "" })).toBe(true);
		expect(isTestExperiment({ exp_id: "exp-1", name: "Prompt v2 against v1" })).toBe(false);
	});
});
