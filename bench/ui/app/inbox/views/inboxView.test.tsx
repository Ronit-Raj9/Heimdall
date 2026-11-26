import "@/lib/i18n";

import { INBOX_LIST_FIXTURE } from "@/components/inbox/inbox.fixtures";
import type { InboxListResponse } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { InboxBody, type InboxBodyProps } from "./inboxView";

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
const base: InboxBodyProps = {
	data: INBOX_LIST_FIXTURE,
	filter: "new",
	onFilter: noop,
	source: null,
	onSource: noop,
	itemId: null,
	onItem: noop,
	panel: null,
};

const rows = (html: string) => [...html.matchAll(/data-testid="inbox-row-([^"]+)"/g)].map((m) => m[1]);

describe("InboxBody", () => {
	it("shows counts by state, the items in the state and asks to pick one", () => {
		const html = renderToStaticMarkup(<InboxBody {...base} />);
		expect(html).toContain('data-testid="inbox-state-filter"');
		expect(html).toMatch(/data-testid="inbox-count-all"[^>]*>6</);
		expect(rows(html)).toEqual(["inc-3f9a1c2b7d10"]);
		expect(html).toContain('data-testid="inbox-select-item"');
		expect(html).toContain("2 new test tasks made from the inbox");
		expect(html).not.toContain('data-testid="mode-badge-synthetic"');
		expect(html).toContain('data-testid="inbox-sources-table"');
		expect(html).not.toContain('data-testid="inbox-ingest-off"');
	});

	it("lists every item under All and filters by source", () => {
		expect(rows(renderToStaticMarkup(<InboxBody {...base} filter="all" />))).toHaveLength(6);
		expect(rows(renderToStaticMarkup(<InboxBody {...base} filter="all" source="edge-otlp" />))).toEqual([
			"inc-7b20c4d9e811",
			"inc-5d8e7f6a1b2c",
		]);
	});

	it("says when a state has no items", () => {
		const data = { ...INBOX_LIST_FIXTURE, items: INBOX_LIST_FIXTURE.items.filter((i) => i.state !== "snoozed") };
		const html = renderToStaticMarkup(<InboxBody {...base} data={data} filter="snoozed" />);
		expect(html).toContain('data-testid="inbox-empty-filtered"');
	});

	it("shows the open item's panel", () => {
		const html = renderToStaticMarkup(<InboxBody {...base} itemId="inc-3f9a1c2b7d10" panel={<div data-testid="panel" />} />);
		expect(html).toContain('data-testid="panel"');
		expect(html).not.toContain('data-testid="inbox-select-item"');
	});

	it("explains heimdall inbox pull and the sources file when the inbox is empty, and notes ingest off", () => {
		const data: InboxListResponse = {
			items: [],
			counts: {},
			sources: [],
			suites: [],
			ingest_enabled: false,
			regression_tasks: 0,
			mode: "live",
		};
		const html = renderToStaticMarkup(<InboxBody {...base} data={data} />);
		expect(html).toContain('data-testid="inbox-empty"');
		expect(html).toContain("From a terminal");
		expect(html).toContain("docs/failure-inbox.md");
		expect(html).toContain('data-testid="inbox-ingest-off"');
		expect(html).toContain('data-testid="inbox-sources-none"');
		expect(html).not.toContain('data-testid="inbox-table"');
	});

	it("reads Go null lists as empty", () => {
		const data = { ...INBOX_LIST_FIXTURE, items: null, sources: null, suites: null } as unknown as InboxListResponse;
		expect(() => renderToStaticMarkup(<InboxBody {...base} data={data} />)).not.toThrow();
	});
});