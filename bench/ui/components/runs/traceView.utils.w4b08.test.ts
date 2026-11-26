import type { StepRecord } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { filterCounts, filterTrace } from "./traceView.utils";

const step = (over: Partial<StepRecord>): StepRecord =>
	({
		seq: 1,
		ts: "2026-11-22T00:00:00Z",
		kind: "agent_event",
		source: "agent_stream",
		proof: "reported",
		turn: 1,
		summary: "",
		...over,
	}) as StepRecord;

// N28: the tools chip counts tool uses, not each use and its answer.
describe("tool use count", () => {
	const steps = [
		step({ seq: 1, event_type: "tool_call", tool_name: "search" }),
		step({ seq: 2, event_type: "tool_result", tool_name: "search" }),
		step({ seq: 3, event_type: "tool_call", tool_name: "read" }),
		step({ seq: 4, event_type: "tool_result", tool_name: "read" }),
	];
	it("counts tool calls, not answers", () => {
		expect(filterCounts(steps).tools).toBe(2);
	});
	it("still lists both kinds under the tools filter", () => {
		expect(filterTrace(steps, "tools", "")).toHaveLength(4);
	});
});