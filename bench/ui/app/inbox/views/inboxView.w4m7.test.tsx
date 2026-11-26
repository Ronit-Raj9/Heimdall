import "@/lib/i18n";

import type { InboxItem, InboxListResponse, InboxSourceStatus } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { InboxBody, sourceNames, type InboxBodyProps } from "./inboxView";

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

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const item = (source: string) => ({ id: `i-${source}`, source }) as InboxItem;

describe("sourceNames (wave 4, p12-6)", () => {
	it("lists the source of the items even when no pull source is configured", () => {
		expect(sourceNames([], [item("webhook")])).toEqual(["webhook"]);
	});

	it("joins configured and item sources, sorted and unique, without empty names", () => {
		const configured = [{ name: "ls", kind: "langsmith" }] as InboxSourceStatus[];
		expect(sourceNames(configured, [item("webhook"), item("ls"), item("")])).toEqual(["ls", "webhook"]);
	});
});

describe("InboxBody unknown item (wave 4, N76)", () => {
	it("empty inbox with ?item=bad says not found", () => {
		const data: InboxListResponse = {
			items: [],
			counts: {},
			sources: [],
			suites: [],
			ingest_enabled: true,
			regression_tasks: 0,
			mode: "live",
		};
		const noop = () => {};
		const props: InboxBodyProps = {
			data,
			filter: "new",
			onFilter: noop,
			source: null,
			onSource: noop,
			itemId: "bad",
			onItem: noop,
			panel: null,
		};
		const html = renderToStaticMarkup(<InboxBody {...props} />);
		expect(html).toContain('data-testid="unknown-link-failure"');
		expect(html).toContain("No failure named bad was found. Showing all failures instead.");
	});
});