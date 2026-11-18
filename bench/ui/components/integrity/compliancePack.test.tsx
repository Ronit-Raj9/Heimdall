import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { CompliancePackLinks } from "./compliancePack";

describe("CompliancePackLinks", () => {
	it("links both formats and states it is not a certificate", () => {
		const html = renderToStaticMarkup(<CompliancePackLinks expId="e_pilot" />);
		expect(html).toContain("/experiments/e_pilot/compliance?format=md");
		expect(html).toContain("/experiments/e_pilot/compliance?format=json");
		expect(html).toContain('download="compliance-e_pilot.md"');
		expect(html).toContain("not a certificate");
	});
});