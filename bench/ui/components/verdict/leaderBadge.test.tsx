import "@/lib/i18n";

import { StatusBadge, isDecision } from "@/components/shell/statusBadge";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

// Fix wave 3, N244 (D12): the setups decision "leader" reads "Leader found" with its icon.
describe("StatusBadge leader", () => {
	it("is a known decision with its own words", () => {
		expect(isDecision("leader")).toBe(true);
		const html = renderToStaticMarkup(<StatusBadge decision="leader" testId="d" />);
		expect(html).toContain("Leader found");
		expect(html).toContain("<svg");
	});
});