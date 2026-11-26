import "@/lib/i18n";

import type { InboxItem, InboxListResponse } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { InboxBody, type InboxBodyProps } from "./inboxView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/traceDraftApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/traceDraftApi")>();
	return { ...actual, useImportInboxSessionMutation: () => [() => ({ unwrap: async () => ({}) }), { isLoading: false }] };
});

const noop = () => {};

function body(items: InboxItem[]): string {
	const data: InboxListResponse = { items, counts: {}, sources: [], suites: [], ingest_enabled: true, regression_tasks: 0, mode: "live" };
	const props: InboxBodyProps = {
		data,
		filter: "all",
		onFilter: noop,
		source: null,
		onSource: noop,
		itemId: null,
		onItem: noop,
		panel: null,
	};
	return renderToStaticMarkup(<InboxBody {...props} />);
}

describe("Import a session on every inbox (group E integration, I32 left undone)", () => {
	it("offers the session import on an inbox that has items", () => {
		const html = body([{ id: "inc-000000000001", source: "webhook", state: "new" } as InboxItem]);
		expect(html).toContain('data-testid="inbox-items"');
		expect(html).toContain('data-testid="inbox-session-import-button"');
		expect(html).toContain("Import a session");
	});

	it("still offers it on the empty inbox, once", () => {
		const html = body([]);
		expect(html.match(/data-testid="inbox-session-import-button"/g)?.length).toBe(1);
	});
});