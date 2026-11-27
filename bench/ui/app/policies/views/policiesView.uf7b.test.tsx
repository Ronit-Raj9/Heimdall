import "@/lib/i18n";

import { starterPolicy } from "@/components/govern/policyForm";
import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { PolicyReadView } from "./policiesView";

describe("PolicyReadView (M7-80)", () => {
	it("shows the rules as sentences before any editing", () => {
		const p = starterPolicy();
		const html = render(<PolicyReadView policy={p} onEdit={() => {}} />);
		expect(html).toContain('data-testid="policy-read-rules"');
		expect(html).not.toContain('data-testid="policy-form"');
		expect(html.match(/<li>/g)?.length).toBe(p.checks.length);
	});
});
