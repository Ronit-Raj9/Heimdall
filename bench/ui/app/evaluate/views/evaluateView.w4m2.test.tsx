import { describe, expect, it } from "vitest";
import { resumeStep, stepHistoryMode } from "./evaluateView";

describe("stepHistoryMode (w4m2)", () => {
	it("pushes a step the person chose and replaces a URL correction", () => {
		expect(stepHistoryMode("user")).toBe("push");
		expect(stepHistoryMode("fix")).toBe("replace");
	});
});
describe("resumeStep (round 6 N1)", () => {
	it("opens a draft with only an agent on step 2, not past What to learn", () => {
		expect(resumeStep({ benchmarks: [], pack: null }, 3)).toBe(2);
		expect(resumeStep({ benchmarks: [], pack: null }, 2)).toBe(2);
	});
	it("opens a draft with benchmarks or a pack on its furthest step", () => {
		expect(resumeStep({ benchmarks: ["hd-harbor-bfcl"], pack: null }, 4)).toBe(4);
		expect(resumeStep({ benchmarks: [], pack: "core" }, 4)).toBe(4);
	});
});