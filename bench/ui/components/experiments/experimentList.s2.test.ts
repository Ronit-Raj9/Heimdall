import { describe, expect, it } from "vitest";
import { anyRunning, deleteMany } from "@/components/experiments/experimentList";
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
});
