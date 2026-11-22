import { describe, expect, it } from "vitest";
import type { SearchMatch } from "@/lib/types/traceTools";
import { hitTarget } from "./traceMatches.utils";

const m = (field: string, seq?: number): SearchMatch => ({ field, seq, text: "x", highlights: [] });

describe("hitTarget", () => {
	it("opens Step by step on the first step match", () => {
		expect(hitTarget({ matches: [m("answer"), m("step", 7), m("step", 9)] })).toEqual({ tab: "timeline", step: 7 });
	});
	it("opens Step by step for a tool call, event or error match", () => {
		for (const f of ["tool_call", "event", "error"]) expect(hitTarget({ matches: [m(f)] })).toEqual({ tab: "timeline" });
	});
	it("opens Output for the answer and Overview for gates and grades", () => {
		expect(hitTarget({ matches: [m("answer")] })).toEqual({ tab: "output" });
		expect(hitTarget({ matches: [m("gate")] })).toEqual({ tab: "overview" });
		expect(hitTarget({ matches: [m("grade")] })).toEqual({ tab: "overview" });
	});
	it("ignores a step match without a step number", () => {
		expect(hitTarget({ matches: [m("step", 0), m("grade")] })).toEqual({ tab: "timeline" });
		expect(hitTarget({ matches: [] })).toEqual({ tab: "timeline" });
	});
});