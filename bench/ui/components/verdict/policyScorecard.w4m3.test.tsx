import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { PolicyScorecard } from "./policyScorecard";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("PolicyScorecard without a release rule set (p05-1)", () => {
	it("speaks to web users, with no terminal flag", () => {
		const html = renderToStaticMarkup(<PolicyScorecard policy={null} expId="e_x" />);
		expect(html).not.toContain("-policy");
		// Round 2 Q-60: the sentence is replaced by a link to Releases, Rules.
		expect(html).toContain("No release rule set applies to this comparison.");
		expect(html).toContain("Pick a rule set");
		expect(html).toContain("policy-scorecard-pick-rules");
	});
});

describe("Compare runs subtitle (p06-7)", () => {
	it("does not name two setups", () => {
		const s = i18n.t("runs.subtitle", { ns: "bench" });
		expect(s).toBe("The same task, run by each setup in the same way, side by side.");
	});
});