import i18n from "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { decisionText } from "./actionsTab";
import { checkSummary } from "./analysisTab";

// Fix wave 3 (H09's leader decision): the Analysis and Actions tabs say "Leader found", never the raw word.
describe("leader decision in words", () => {
	it("is in the Analysis tab's verdict words", () => {
		const t = i18n.getFixedT("en", "analysis");
		expect(checkSummary(t, { decision: "leader", action: "no_decision" })).toBe("Leader found");
	});
	it("is in the Actions tab's decisions", () => {
		const words = { score: "s", improved: "i", regressed: "r", inconclusive: "n", invalid: "x", leader: "Leader found" };
		expect(decisionText("leader", words)).toBe("Leader found");
		expect(decisionText("LEADER", words)).toBe("Leader found");
	});
});