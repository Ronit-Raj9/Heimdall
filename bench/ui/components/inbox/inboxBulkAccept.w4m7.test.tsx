import "@/lib/i18n";

import { mutation } from "@/lib/testing/render";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { InboxBulkAccept } from "./inboxBulkAccept";

vi.mock("@/lib/store/apis/inboxApi", () => ({
	useBulkAcceptInboxItemsMutation: () => mutation({ data: { accepted: ["a", "b"], failed: [] } }),
}));

describe("InboxBulkAccept (wave 4, p13-1)", () => {
	it("keeps the result line when the last open items were accepted", () => {
		const html = renderToStaticMarkup(<InboxBulkAccept items={[]} suites={[]} />);
		expect(html).toContain('data-testid="inbox-bulk-result"');
		expect(html).toContain("Accepted 2");
		expect(html).toContain("Nothing else is waiting to be accepted.");
		expect(html).not.toContain('data-testid="inbox-bulk-submit"');
	});
});