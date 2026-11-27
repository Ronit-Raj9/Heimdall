import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { INBOX_ACCEPT_FIXTURE, INBOX_ACCEPTED_DETAIL_FIXTURE, INBOX_LIST_FIXTURE, INBOX_NEW_DETAIL_FIXTURE } from "./inbox.fixtures";
import { InboxItemDetailView, InboxPreview } from "./inboxDetail";
import { InboxDraftResult } from "./inboxDraft";
import { InboxEmpty, InboxIngestOffNote, InboxSourcesTable } from "./inboxSources";

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

// A plain anchor stands in for the router's Link, so the target URL is visible in the markup.
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("InboxPreview", () => {
	it("shows a planted script tag as text, never as markup", () => {
		const planted = 'user said: <script>alert("x")</script> and <img src=x onerror=alert(1)>';
		const html = renderToStaticMarkup(<InboxPreview text={planted} />);
		expect(html).toContain("&lt;script&gt;alert(&quot;x&quot;)&lt;/script&gt;");
		expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;");
		expect(html).not.toContain("<script");
		expect(html).not.toContain("<img");
		expect(html).toMatch(/^<pre[^>]*data-testid="inbox-preview"/);
	});

	it("says when no preview was stored", () => {
		expect(renderToStaticMarkup(<InboxPreview text="" />)).toContain('data-testid="inbox-preview-empty"');
	});
});

describe("InboxItemDetailView", () => {
	it("shows the redacted preview, signature parts, traces and redaction counts of a new item", () => {
		const html = renderToStaticMarkup(<InboxItemDetailView detail={INBOX_NEW_DETAIL_FIXTURE} />);
		expect(html).toContain('data-testid="inbox-preview"');
		expect(html).toContain("[REDACTED:email]");
		expect(html).toContain("f_3f9a1c2b7d10aa00");
		expect(html).toContain("q_ab12cd34ef56");
		expect(html).toContain("issue_refund");
		expect(html).toContain("timeout after &lt;n&gt;s");
		for (const id of ["abc101", "abc117", "abc123"]) expect(html).toContain(`data-testid="inbox-detail-trace-${id}"`);
		expect(html).toContain("Removed before saving: 1 email.");
		expect(html).toContain("MCP task in benchmark support");
		expect(html).toContain('href="https://cloud.langfuse.com/project/p1/traces/abc123"');
		expect(html).not.toContain('data-testid="inbox-detail-task"');
	});

	it("shows what an accepted item became, how its task does and that it recurred", () => {
		const html = renderToStaticMarkup(<InboxItemDetailView detail={INBOX_ACCEPTED_DETAIL_FIXTURE} />);
		expect(html).toContain('data-testid="inbox-detail-task"');
		expect(html).toContain("Turned into task warranty-period in benchmark support");
		expect(html).toContain("solved in e_123 (3 of 3 runs)");
		expect(html).toContain('href="/experiments/e_123"');
		expect(html).toContain('data-testid="inbox-detail-recurrences"');
		expect(html).toContain(">ready<");
		// No link on the item or its traces: nothing to open.
		expect(html).not.toContain('target="_blank"');
	});

	it("shows a reject reason and the duplicate it points at", () => {
		const dup = INBOX_LIST_FIXTURE.items.find((i) => i.state === "duplicate")!;
		const html = renderToStaticMarkup(<InboxItemDetailView detail={{ item: dup, preview: "x" }} />);
		expect(html).toContain("Same failure as inc-3f9a1c2b7d10.");
		expect(html).toContain("Rejected: same refund timeout");
	});

	it("renders the actions slot", () => {
		const html = renderToStaticMarkup(<InboxItemDetailView detail={INBOX_NEW_DETAIL_FIXTURE} actions={<p data-testid="slot">x</p>} />);
		expect(html).toContain('data-testid="slot"');
	});
});

describe("InboxDraftResult", () => {
	it("says plainly that the draft still needs a person, with the files and next steps", () => {
		const html = renderToStaticMarkup(<InboxDraftResult draft={INBOX_ACCEPT_FIXTURE.draft} />);
		expect(html).toContain("Draft task support/refund-timeout created");
		expect(html).toContain("This is a draft, not a finished test.");
		expect(html).toContain("EDIT ME");
		expect(html).toContain("instruction.md");
		expect(html).toContain("verifier/tests/checks.py");
		expect(html).toContain("bifrost-bench task validate support/refund-timeout");
		expect(html).toContain("bifrost-bench task admit support/refund-timeout");
		expect(html).toContain('data-testid="inbox-draft-warnings"');
	});

	it("leaves out warnings when there are none", () => {
		const { warnings: _, ...draft } = INBOX_ACCEPT_FIXTURE.draft;
		expect(renderToStaticMarkup(<InboxDraftResult draft={draft} />)).not.toContain("inbox-draft-warnings");
	});
});

describe("sources and empty state", () => {
	it("lists each source with its last pull and last error", () => {
		const html = renderToStaticMarkup(<InboxSourcesTable sources={INBOX_LIST_FIXTURE.sources} />);
		expect(html).toContain('data-testid="inbox-source-prod-langfuse"');
		expect(html).toContain('data-testid="inbox-source-edge-otlp"');
		expect(html).toContain("read traces/edge.jsonl: no such file or directory");
		expect(html).toContain(">12<");
	});

	it("says when no source is configured", () => {
		expect(renderToStaticMarkup(<InboxSourcesTable sources={[]} />)).toContain("Nothing connected yet.");
	});

	it("explains how items get in", () => {
		const html = renderToStaticMarkup(<InboxEmpty />);
		expect(html).toContain("From a terminal");
		expect(html).toContain("Connect a source"); // M7-2 first-use empty state
	});

	it("notes when push ingest is off", () => {
		expect(renderToStaticMarkup(<InboxIngestOffNote />)).toContain("does not accept failures sent in");
	});
});