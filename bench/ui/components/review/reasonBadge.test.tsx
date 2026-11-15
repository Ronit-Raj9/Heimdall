import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReasonBadge } from "./reasonBadge";

describe("ReasonBadge", () => {
	const cases = [
		{ reason: "split_votes", text: "Split judge votes" },
		{ reason: "veto", text: "Veto gate fired" },
		{ reason: "flagged", text: "Flagged" },
		{ reason: "brand_new_reason", text: "brand_new_reason" },
	];
	for (const c of cases) {
		it(c.reason, () => {
			const html = renderToStaticMarkup(<ReasonBadge reason={c.reason} testId="r" />);
			expect(html).toContain(`>${c.text}<`);
			expect(html).toContain(`data-reason="${c.reason}"`);
		});
	}
});