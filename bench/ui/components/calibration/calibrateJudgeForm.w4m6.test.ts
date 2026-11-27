import "@/lib/i18n";

import i18n from "i18next";
import { describe, expect, it } from "vitest";
import { callsText } from "./calibrateJudgeForm";

const t = i18n.getFixedT("en", "w4m6") as never;

describe("grader call count (p11-6)", () => {
	it("waits for the estimate and uses plural words", () => {
		expect(callsText(t, null)).toBe("Call count shows after Estimate cost.");
		expect(callsText(t, 1)).toBe("About 1 grader call, billed to your key.");
		expect(callsText(t, 16)).toBe("About 16 grader calls, billed to your key.");
	});
});