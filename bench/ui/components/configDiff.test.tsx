import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ConfigDiff } from "./configDiff";

describe("ConfigDiff", () => {
	it("renders one row per change, null as unset and non-strings as JSON", () => {
		const html = renderToStaticMarkup(
			<ConfigDiff
				changes={[
					{ path: "notes.user_file", a: null, b: "fixtures/notes/codewins.md" },
					{ path: "budgets.turns", a: 60, b: 80 },
				]}
			/>,
		);
		expect(html).toContain('data-testid="config-diff-row-notes.user_file"');
		expect(html).toContain(">unset<");
		expect(html).toContain(">fixtures/notes/codewins.md<");
		expect(html).toContain(">60<");
		expect(html).toContain(">80<");
	});

	it("renders values as inert text", () => {
		const html = renderToStaticMarkup(<ConfigDiff changes={[{ path: "prompt", a: "<b>x</b>", b: "y" }]} />);
		expect(html).not.toContain("<b>x</b>");
		expect(html).toContain("&lt;b&gt;x&lt;/b&gt;");
	});

	it("says so when the arms do not differ", () => {
		expect(renderToStaticMarkup(<ConfigDiff changes={[]} />)).toContain('data-testid="config-diff-empty"');
	});
});