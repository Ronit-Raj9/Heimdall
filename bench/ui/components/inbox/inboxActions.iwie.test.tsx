import "@/lib/i18n";

import type { InboxSession } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { INBOX_LIST_FIXTURE, INBOX_NEW_DETAIL_FIXTURE } from "./inbox.fixtures";
import { AcceptForm, quoteOffset, scrollSessionTo } from "./inboxActions";

vi.mock("@/lib/store/apis/traceDraftApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/traceDraftApi")>();
	return { ...actual, useGetInboxProposalQuery: () => ({ data: undefined, refetch: () => undefined }) };
});
vi.mock("@/lib/store/apis/inboxApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/inboxApi")>();
	return { ...actual, useAcceptInboxItemMutation: () => [() => ({ unwrap: async () => ({}) }), { isLoading: false }] };
});
vi.mock("@/lib/store/apis/jobRunApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/jobRunApi")>();
	return { ...actual, useCreateJobMutation: () => [() => ({ unwrap: async () => ({ id: "j" }) }), { isLoading: false }] };
});
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/platformApi")>();
	return { ...actual, useGetJobQuery: () => ({ data: undefined }) };
});

const noop = () => {};
const item = INBOX_NEW_DETAIL_FIXTURE.item;
const suites = INBOX_LIST_FIXTURE.suites;
// Made-up session (never a real session file).
const session: InboxSession = { requests: ["Refund order A-1 please"], final_answer: "I could not." };

describe("AcceptForm review bar (group E integration, I32 snippet)", () => {
	it("shows the review bar for an item once a benchmark is chosen", () => {
		const html = renderToStaticMarkup(
			<AcceptForm item={item} session={session} suites={suites} initialValues={{ suite: suites[0].id }} onSubmit={noop} />,
		);
		expect(html).toContain('data-testid="proposal-bar"');
	});

	it("shows no review bar while no benchmark is chosen", () => {
		const html = renderToStaticMarkup(
			<AcceptForm
				item={{ ...item, suggested_suite: undefined }}
				session={session}
				suites={suites}
				initialValues={{ suite: "" }}
				onSubmit={noop}
			/>,
		);
		expect(html).not.toContain('data-testid="proposal-bar"');
	});
});

const PREVIEW = [
	"# Flagged session s1",
	"",
	"## Conversation",
	"",
	"1. user:",
	"    Refund order A-1",
	"",
	"## Tool calls",
	"",
	"1. lookup (ok)",
	"    arguments: {}",
	"",
	"2. refund_order (failed)",
	"    arguments: {}",
	"",
].join("\n");

describe("quote click scrolls the session panel (group E integration, I32 left undone)", () => {
	it("finds a call by its seq under Tool calls, and a turn in the conversation", () => {
		expect(PREVIEW.slice(quoteOffset(PREVIEW, 2)).startsWith("2. refund_order")).toBe(true);
		expect(PREVIEW.slice(quoteOffset(PREVIEW, 1)).startsWith("1. lookup")).toBe(true);
		expect(PREVIEW.slice(quoteOffset(PREVIEW, 0)).startsWith("## Conversation")).toBe(true);
		expect(quoteOffset(PREVIEW, 9)).toBe(-1);
		expect(quoteOffset("no sections", 1)).toBe(-1);
	});

	it("brings the panel into view and scrolls it to the quoted call", () => {
		const scrollIntoView = vi.fn();
		const panel = { textContent: PREVIEW, scrollHeight: 1000, scrollTop: 0, scrollIntoView };
		const root = { querySelector: (sel: string) => (sel === '[data-testid="inbox-preview"]' ? panel : null) } as unknown as ParentNode;
		scrollSessionTo(2, root);
		expect(scrollIntoView).toHaveBeenCalledOnce();
		expect(panel.scrollTop).toBeGreaterThan(0);
	});

	it("does nothing when the page shows no session panel", () => {
		const root = { querySelector: () => null } as unknown as ParentNode;
		expect(() => scrollSessionTo(1, root)).not.toThrow();
	});
});