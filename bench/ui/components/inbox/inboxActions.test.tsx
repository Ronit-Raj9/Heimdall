import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { acceptDefaults, acceptRequestFrom } from "./inbox.utils";
import { INBOX_LIST_FIXTURE, INBOX_NEW_DETAIL_FIXTURE } from "./inbox.fixtures";
import { AcceptForm, InboxActions, RejectForm, SnoozeForm, type InboxActionsProps } from "./inboxActions";

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

const noop = () => {};
const item = INBOX_NEW_DETAIL_FIXTURE.item;
const suites = INBOX_LIST_FIXTURE.suites;
const base: InboxActionsProps = { item, suites, mode: "live", onAccept: noop, onReject: noop, onSnooze: noop };

/** The opening tag of the element with the test id. */
function tag(html: string, testId: string): string {
	const match = html.match(new RegExp(`<[a-z]+[^>]*data-testid="${testId}"[^>]*>`));
	expect(match, testId).not.toBeNull();
	return match![0];
}

/** The option elements inside the select with the test id. */
function optionsOf(html: string, testId: string): string {
	const match = html.match(new RegExp(`data-testid="${testId}"[^>]*>(.*?)</select>`));
	expect(match, testId).not.toBeNull();
	return match![1];
}

describe("AcceptForm", () => {
	it("starts on the suggested suite and kind and offers every suite and kind", () => {
		const html = renderToStaticMarkup(<AcceptForm item={item} suites={suites} onSubmit={noop} />);
		const suiteOptions = optionsOf(html, "inbox-accept-suite");
		expect(suiteOptions).toContain('<option value="support" selected="">support (template mcp)</option>');
		expect(suiteOptions).toContain('<option value="faq">faq (template qa)</option>');
		const kindOptions = optionsOf(html, "inbox-accept-kind");
		expect(kindOptions).toContain('<option value="mcp" selected="">MCP</option>');
		for (const kind of ["qa", "tool-use", "memory"]) expect(kindOptions).toContain(`<option value="${kind}">`);
		expect(html).toContain("Suggested: MCP.");
		expect(tag(html, "inbox-accept-submit")).not.toContain('disabled=""');
	});

	it("posts the suite, the kind and the typed task id, and nothing else", () => {
		// The form submits acceptRequestFrom(values); inboxApi.test.ts follows this body onto the wire.
		const values = { ...acceptDefaults(item, suites), taskId: "refund-timeout" };
		expect(acceptRequestFrom(values)).toEqual({ suite: "support", kind: "mcp", task_id: "refund-timeout" });
		expect(acceptRequestFrom(acceptDefaults(item, suites))).toEqual({ suite: "support", kind: "mcp" });
	});

	it("asks for a suite when none is picked", () => {
		const html = renderToStaticMarkup(
			<AcceptForm item={{ ...item, suggested_suite: undefined }} suites={suites} showErrors onSubmit={noop} />,
		);
		expect(html).toContain('data-testid="inbox-accept-error-suite"');
		expect(tag(html, "inbox-accept-suite")).toContain('aria-invalid="true"');
	});

	it("says so and stays disabled when no suite can take tasks", () => {
		const html = renderToStaticMarkup(<AcceptForm item={item} suites={[]} onSubmit={noop} />);
		expect(html).toContain('data-testid="inbox-accept-no-suites"');
		expect(tag(html, "inbox-accept-submit")).toContain('disabled=""');
	});
});

describe("RejectForm", () => {
	it("requires a reason", () => {
		const html = renderToStaticMarkup(<RejectForm itemId={item.id} showErrors onSubmit={noop} />);
		expect(html).toContain('data-testid="inbox-reject-error-reason"');
		expect(html).toContain("Say why the item is rejected.");
		expect(tag(html, "inbox-reject-reason")).toContain('aria-invalid="true"');
		expect(tag(html, "inbox-reject-reason")).toContain('required=""');
	});

	it("shows no error once a reason is given", () => {
		const html = renderToStaticMarkup(
			<RejectForm itemId={item.id} showErrors initialValues={{ reason: "expected behaviour" }} onSubmit={noop} />,
		);
		expect(html).not.toContain("inbox-reject-error-");
	});

	it("refuses the item as its own duplicate", () => {
		const html = renderToStaticMarkup(
			<RejectForm itemId={item.id} showErrors initialValues={{ reason: "same", duplicateOf: item.id }} onSubmit={noop} />,
		);
		expect(html).toContain('data-testid="inbox-reject-error-duplicate"');
	});
});

describe("SnoozeForm", () => {
	it("defaults to 7 days and flags a value outside 1 to 90", () => {
		expect(tag(renderToStaticMarkup(<SnoozeForm onSubmit={noop} />), "inbox-snooze-days")).toContain('value="7"');
		const html = renderToStaticMarkup(<SnoozeForm initialDays="120" showErrors onSubmit={noop} />);
		expect(html).toContain('data-testid="inbox-snooze-error-days"');
	});
});

describe("InboxActions", () => {
	it("offers accept as the one primary, reject and snooze under More ways, with the write token on a live server", () => {
		const html = renderToStaticMarkup(<InboxActions {...base} />);
		// Reject and snooze are menu items (M7-4): only the accept button and the menu trigger render closed.
		expect(html).toContain('data-testid="inbox-action-accept"');
		expect(html).toContain('data-testid="inbox-action-more-button"');
		expect(tag(html, "inbox-token-input")).toContain('type="password"');
		expect(html).toContain('data-testid="inbox-accept-form"');
		expect(html).not.toContain('data-testid="inbox-actions-read-only"');
	});

	it("opens the chosen form", () => {
		expect(renderToStaticMarkup(<InboxActions {...base} initialAction="reject" />)).toContain('data-testid="inbox-reject-form"');
		expect(renderToStaticMarkup(<InboxActions {...base} initialAction="snooze" />)).toContain('data-testid="inbox-snooze-form"');
	});

	for (const mode of ["recorded", "synthetic"] as const) {
		it(`is read-only in ${mode} mode`, () => {
			const html = renderToStaticMarkup(<InboxActions {...base} mode={mode} />);
			expect(html).toContain('data-testid="inbox-actions-read-only"');
			expect(html).toContain(`runs in ${mode} mode`);
			expect(tag(html, "inbox-accept-submit")).toContain('disabled=""');
		});
	}

	it("takes no action on an item that is no longer new or snoozed", () => {
		const accepted = INBOX_LIST_FIXTURE.items.find((i) => i.state === "accepted")!;
		const html = renderToStaticMarkup(<InboxActions {...base} item={accepted} />);
		expect(html).toContain('data-testid="inbox-actions-closed"');
		expect(html).toContain("This failure is accepted.");
		expect(html).not.toContain("inbox-accept-form");
	});

	it("shows the server's refusal and what a snooze did", () => {
		const html = renderToStaticMarkup(
			<InboxActions
				{...base}
				submitError="item inc-3f9a1c2b7d10 is accepted, not new or snoozed"
				done={{ action: "snooze", item: { ...item, state: "snoozed", snoozed_until: "2026-11-25T00:00:00Z" } }}
			/>,
		);
		expect(html).toContain("The server refused: item inc-3f9a1c2b7d10 is accepted, not new or snoozed");
		expect(html).toContain('data-testid="inbox-done-snooze"');
	});
});