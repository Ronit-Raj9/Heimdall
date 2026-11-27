import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { IngestTokensNote } from "./apiTokensTab";

describe("access tokens: where ingest tokens live (wave 4, p02-7)", () => {
	it("links to the Inbox page", () => {
		const html = renderToStaticMarkup(<IngestTokensNote />);
		expect(html).toContain('href="/failures"');
		expect(html).toContain("Ingest tokens for traces and failures are managed on the Inbox page.");
	});
});