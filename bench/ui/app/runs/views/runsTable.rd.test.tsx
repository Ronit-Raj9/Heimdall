import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { OutcomePill } from "./runsTable";

describe("runs list status (X-37)", () => {
	it("shows a dot and the word, never a filled pill", () => {
		const html = renderToStaticMarkup(<OutcomePill row={{ sealed: true, outcome: "fail", run_id: "r1" }} />);
		expect(html).toContain('data-slot="status-dot"');
		expect(html).toContain('data-tone="bad"');
		expect(html).not.toMatch(/bg-danger-soft|rounded-full px-2/);
	});
});
