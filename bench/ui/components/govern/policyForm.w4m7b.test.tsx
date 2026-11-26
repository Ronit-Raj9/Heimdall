import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PolicyForm, starterPolicy } from "./policyForm";

describe("rule set name check", () => {
	it("an empty name shows why and marks the box", () => {
		const html = renderToStaticMarkup(<PolicyForm value={{ ...starterPolicy(), name: "" }} onChange={() => undefined} />);
		expect(html).toContain("Enter the rule set name.");
		expect(html).toMatch(/aria-invalid="true"[^>]*data-testid="policy-name"|data-testid="policy-name"[^>]*aria-invalid="true"/);
	});

	it("a good name shows no message", () => {
		const html = renderToStaticMarkup(<PolicyForm value={starterPolicy()} onChange={() => undefined} />);
		expect(html).not.toContain("policy-name-problem");
	});
});