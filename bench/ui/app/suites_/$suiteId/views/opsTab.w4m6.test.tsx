import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { render } from "@/lib/testing/render";
import { DisabledRun } from "./opsTab";

describe("disabled job buttons (p11-7d)", () => {
	it("shows the reason as text under the button, not as a tooltip", () => {
		const html = render(<DisabledRun label="Preview candidates" reason="Pick a repository first." testId="suite-ops-mine-disabled" />);
		expect(html).toContain(">Pick a repository first.<");
		expect(html).not.toContain('title="Pick a repository first."');
		expect(html).toContain('aria-describedby="suite-ops-mine-disabled-why"');
	});
});