import { describe, expect, it } from "vitest";
import { stepHistoryMode } from "./evaluateView";

describe("stepHistoryMode (w4m2)", () => {
	it("pushes a step the person chose and replaces a URL correction", () => {
		expect(stepHistoryMode("user")).toBe("push");
		expect(stepHistoryMode("fix")).toBe("replace");
	});
});