import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { INBOX_LIST_FIXTURE, INBOX_NEW_DETAIL_FIXTURE } from "./inbox.fixtures";
import { InboxActions, type InboxActionsProps } from "./inboxActions";

// The review bar and the session import call the API; these tests render without a store (group E integration).
vi.mock("@/lib/store/apis/traceDraftApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/traceDraftApi")>()),
	useGetInboxProposalQuery: () => ({ data: undefined, refetch: () => undefined }),
	useImportInboxSessionMutation: () => [() => ({ unwrap: async () => ({}) }), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/inboxApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/inboxApi")>()),
	useAcceptInboxItemMutation: () => [() => ({ unwrap: async () => ({}) }), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/jobRunApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/jobRunApi")>()),
	useCreateJobMutation: () => [() => ({ unwrap: async () => ({ id: "j" }) }), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useGetJobQuery: () => ({ data: undefined }),
}));

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