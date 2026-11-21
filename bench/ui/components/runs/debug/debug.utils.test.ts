import { describe, expect, it } from "vitest";
import type { StepRecord } from "@/lib/types/bench";
import { alignTraces, callsByRequest, countdown, parseCallList, parseNames, stepIndex } from "./debug.utils";

const step = (seq: number, turn: number, kind: StepRecord["kind"], tool?: string): StepRecord =>
	({ seq, ts: "2026-11-22T00:00:00Z", kind, source: "gateway", proof: "trusted", turn, summary: "", tool_name: tool }) as unknown as StepRecord;

describe("debug utils", () => {
	it("parses call lists", () => {
		expect(parseCallList("4, 1,4 7")).toEqual([1, 4, 7]);
		expect(parseCallList("")).toEqual([]);
		expect(parseCallList("0")).toBeNull();
		expect(parseCallList("2,x")).toBeNull();
		expect(parseNames("bash, edit bash")).toEqual(["bash", "edit"]);
	});
	it("steps with j and k", () => {
		expect(stepIndex(-1, 1, 3)).toBe(0);
		expect(stepIndex(-1, -1, 3)).toBe(2);
		expect(stepIndex(2, 1, 3)).toBe(2);
		expect(stepIndex(0, -1, 3)).toBe(0);
		expect(stepIndex(0, 1, 0)).toBe(-1);
	});
	it("counts down", () => {
		expect(countdown("2026-11-22T00:01:05Z", Date.parse("2026-11-22T00:00:00Z"))).toBe("1:05");
		expect(countdown("2026-11-22T00:00:00Z", Date.parse("2026-11-22T00:01:00Z"))).toBe("0:00");
	});
	it("keys calls by request id", () => {
		const m = callsByRequest([{ call: 1, request_id: "a", status: 200, action: "pass" }, { call: 2, status: 200, action: "pass" }]);
		expect(m.get("a")?.call).toBe(1);
		expect(m.size).toBe(1);
	});
	it("aligns two traces by model call", () => {
		const left = [step(1, 1, "model_turn"), step(2, 1, "tool_request", "bash"), step(3, 2, "model_turn"), step(4, 2, "command")];
		const right = [step(1, 1, "model_turn"), step(2, 1, "tool_request", "bash"), step(3, 2, "model_turn"), step(4, 2, "tool_request", "edit"), step(5, 3, "model_turn")];
		const { rows, firstDiff } = alignTraces(left, right);
		expect(rows.map((r) => r.turn)).toEqual([1, 2, 3]);
		expect(rows[0].same).toBe(true);
		expect(rows[1].same).toBe(false);
		expect(rows[2].left).toEqual([]);
		expect(firstDiff).toBe(2);
		expect(alignTraces(left, left).firstDiff).toBeNull();
	});
});
