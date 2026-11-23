import { describe, expect, it } from "vitest";
import { stepParamSelection } from "./traceView.utils";

// Fix wave 3 (H11 "Show that step" with H10's trace): a changed ?step= selects the new step while the trace is open.
describe("stepParamSelection", () => {
	it("selects a newly asked-for step", () => {
		expect(stepParamSelection(3, 7, 3)).toBe(7);
		expect(stepParamSelection(undefined, 4, null)).toBe(4);
		expect(stepParamSelection(3, 7, 5)).toBe(7);
	});
	it("keeps the current selection when the step did not change or was removed", () => {
		expect(stepParamSelection(3, 3, 5)).toBe(5);
		expect(stepParamSelection(3, undefined, 5)).toBe(5);
		expect(stepParamSelection(undefined, undefined, null)).toBeNull();
	});
});