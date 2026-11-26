import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { EXPLAIN_FIXTURE } from "./improve.fixtures";
import { VersionExplainView } from "./versionExplain";

describe("VersionExplainView", () => {
	it("shows each changed file with its diff and each edit with its reason, evidence count and result", () => {
		const html = render(<VersionExplainView explain={EXPLAIN_FIXTURE} />);
		expect(html).toContain("/home/agent/AGENTS.md");
		expect(html).toContain("level: 3");
		expect(html).toContain("Runs stopped before the tests ran");
		expect(html).toContain("from 3 practice runs");
		expect(html).toContain("Result: applied");
		expect(html).toContain("Most failures ended before the tests ran.");
	});
});