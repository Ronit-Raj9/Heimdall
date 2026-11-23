import type { StepRecord } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { filterCounts, filterTrace, isErrorStep, isMismatchStep } from "./traceView.utils";

const step = (over: Partial<StepRecord>): StepRecord =>
	({
		seq: 1,
		ts: "2026-11-22T00:00:00Z",
		kind: "model_turn",
		source: "gateway",
		proof: "verified",
		turn: 1,
		summary: "",
		...over,
	}) as StepRecord;

// N240: a witness conflict is a mismatch between two records of the run, not an error of the agent.
describe("errors and mismatches in a trace", () => {
	const conflict = step({
		seq: 1,
		kind: "agent_event",
		source: "agent_stream",
		proof: "partial",
		summary: "tool call exec_command",
		conflict: "the agent stream reports a tool call the complete gateway log does not have",
	});
	const failed = step({ seq: 3, is_error: true });
	const exit = step({ seq: 4, kind: "command", exit_code: 2 });
	const steps = [conflict, step({ seq: 2 }), failed, exit];
	it("a conflict-only step is a mismatch, not an error", () => {
		expect(isErrorStep(conflict)).toBe(false);
		expect(isMismatchStep(conflict)).toBe(true);
		expect(isMismatchStep(failed)).toBe(false);
		expect(isErrorStep(failed)).toBe(true);
		expect(isErrorStep(exit)).toBe(true);
	});
	it("the first error is the first flagged step and the chips count apart", () => {
		expect(steps.find(isErrorStep)?.seq).toBe(3);
		const counts = filterCounts(steps);
		expect(counts.errors).toBe(2);
		expect(counts.mismatches).toBe(1);
		expect(filterTrace(steps, "errors", "").map((s) => s.seq)).toEqual([3, 4]);
		expect(filterTrace(steps, "mismatches", "").map((s) => s.seq)).toEqual([1]);
	});
});