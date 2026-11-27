import { describe, expect, it } from "vitest";
import type { StepRecord, ToolCall } from "@/lib/types/bench";
import { bySeq, localStepTime, toolErrorCount } from "./traceView";

describe("trace order, times and error count (M5-10, M5-11)", () => {
	it("sorts steps by seq", () => {
		const steps = [{ seq: 3 }, { seq: 1 }, { seq: 2 }] as StepRecord[];
		expect(bySeq(steps).map((s) => s.seq)).toEqual([1, 2, 3]);
	});

	it("puts agent-reported rows after the witnessed row they report on, the rest after every witnessed row (F3-10)", () => {
		const steps = [
			{ seq: 1, source: "agent_reported", witness_of: 4 },
			{ seq: 2, source: "agent_reported" },
			{ seq: 3, source: "gateway" },
			{ seq: 4, source: "tool_proxy" },
			{ seq: 5, source: "gateway" },
		] as StepRecord[];
		expect(bySeq(steps).map((s) => s.seq)).toEqual([3, 4, 1, 5, 2]);
	});

	it("formats a time in the given locale, keeping bad input", () => {
		expect(localStepTime("2026-11-20T17:13:46Z", "en-GB")).toMatch(/^\d\d:\d\d:46$/);
		expect(localStepTime("nope")).toBe("nope");
	});

	it("counts tool server rows answered with an error", () => {
		const calls = [
			{ is_error: true, status: 405 },
			{ is_error: false, status: 502 },
			{ is_error: false, status: 200 },
			{ is_error: true, kind: "transport" },
		] as ToolCall[];
		expect(toolErrorCount(calls)).toBe(3);
		expect(toolErrorCount(undefined)).toBe(0);
	});
});
