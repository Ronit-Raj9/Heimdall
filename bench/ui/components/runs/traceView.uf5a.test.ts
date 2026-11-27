import { describe, expect, it } from "vitest";
import type { StepRecord, ToolCall } from "@/lib/types/bench";
import { bySeq, localStepTime, toolErrorCount } from "./traceView";

describe("trace order, times and error count (M5-10, M5-11)", () => {
	it("sorts steps by seq", () => {
		const steps = [{ seq: 3 }, { seq: 1 }, { seq: 2 }] as StepRecord[];
		expect(bySeq(steps).map((s) => s.seq)).toEqual([1, 2, 3]);
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
