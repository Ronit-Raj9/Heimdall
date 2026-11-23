import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReasonBadge } from "./reasonBadge";

describe("ReasonBadge", () => {
	const cases = [
		{ reason: "split_votes", text: "AI grader votes disagreed" },
		{ reason: "veto", text: "Safety check failed the run" },
		{ reason: "flagged", text: "Flagged by someone" },
		{ reason: "invalidated", text: "Thrown out by a safety check" },
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
describe("ReasonBadge invalidated", () => {
	it("names the safety check that threw the run out", () => {
		const html = renderToStaticMarkup(<ReasonBadge reason="invalidated" detail={["model check: served by another model"]} />);
		expect(html).toContain(">Thrown out by a safety check: model check: served by another model<");
		expect(html).not.toContain(">invalidated<");
	});
});