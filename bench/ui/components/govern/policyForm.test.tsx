import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { PolicyForm, isListedCondition, starterPolicy } from "./policyForm";

describe("PolicyForm conditions", () => {
	it("offers the PS2 faults as a dropdown on every rule", () => {
		const html = render(<PolicyForm value={starterPolicy()} onChange={() => undefined} />);
		expect(html).toContain('data-testid="policy-check-condition-0"');
		expect(html).toContain("when the model is down");
		expect(html).toContain("when a tool breaks");
		expect(html).toContain("with a misleading instruction");
		expect(html).toContain('value="heimdall-broken-tool"');
	});

	it("keeps a benchmark's own condition id as a custom choice", () => {
		expect(isListedCondition("all")).toBe(true);
		expect(isListedCondition(undefined)).toBe(true);
		expect(isListedCondition("model-outage")).toBe(false);
		const p = starterPolicy();
		p.checks[0] = { ...p.checks[0], condition: "model-outage" };
		expect(render(<PolicyForm value={p} onChange={() => undefined} />)).toContain("on model-outage");
	});
});
