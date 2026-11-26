import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { FirstLookNote } from "./firstLookLauncher";

// p02-4: the note names the picked task count, and is hidden when the count is unknown.
describe("FirstLookNote", () => {
	it("note shows the picked task count", () => {
		const html = renderToStaticMarkup(<FirstLookNote tasks={2} />);
		expect(html).toContain("First look: 2 tasks. Too small for a verdict. Run the full benchmark for one.");
	});
	it("shows nothing without a count", () => {
		expect(renderToStaticMarkup(<FirstLookNote />)).toBe("");
	});
});