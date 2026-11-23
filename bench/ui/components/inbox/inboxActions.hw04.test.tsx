import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { INBOX_LIST_FIXTURE, INBOX_NEW_DETAIL_FIXTURE } from "./inbox.fixtures";
import { InboxActions, type InboxActionsProps } from "./inboxActions";

// Fix wave 3, N286: the Failure inbox action form has no write-token box on the hosted server.

const noop = () => {};
const base: InboxActionsProps = {
	item: INBOX_NEW_DETAIL_FIXTURE.item,
	suites: INBOX_LIST_FIXTURE.suites,
	mode: "live",
	onAccept: noop,
	onReject: noop,
	onSnooze: noop,
};

describe("TestInboxActionFormHasNoTokenOnHosted", () => {
	it("shows no token box to a signed-in member", () => {
		const html = render(<InboxActions {...base} />, hostedSession("member"));
		expect(html).toContain('data-testid="inbox-action-switch"');
		expect(html).not.toContain('data-testid="inbox-token-input"');
	});

	it("keeps the token box in local mode", () => {
		expect(render(<InboxActions {...base} />)).toContain('data-testid="inbox-token-input"');
	});
});