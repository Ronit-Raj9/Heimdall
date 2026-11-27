import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { InboxIngestOffNote } from "./inboxSources";

// N78: the ingest-off note is page text, not a "From a terminal" block, so it names no command.
describe("ingest off note", () => {
	it("says how failures arrive without a command line", () => {
		const html = renderToStaticMarkup(<InboxIngestOffNote />);
		expect(html).toContain("they arrive only when Heimdall pulls them: press Pull now on a connected tool.");
		expect(html).not.toMatch(/heimdall inbox|bifrost-bench/);
	});
});
