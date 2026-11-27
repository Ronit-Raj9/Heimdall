import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { InboxConnectAnotherLink, InboxEmpty, InboxSourcesTable } from "./inboxSources";

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

vi.mock("@/components/jobs/jobRunButton", () => ({
	JobRunButton: (p: { kind: string; label: string; confirm?: string; params?: object }) => (
		<button type="button" data-kind={p.kind} data-confirm={p.confirm} data-params={JSON.stringify(p.params)}>
			{p.label}
		</button>
	),
}));

const source = { name: "zz-src", kind: "langsmith" };

describe("InboxSourcesTable (wave 4, p13-2)", () => {
	it("gives each saved source a Remove control that asks first and a link to connect another tool", () => {
		const html = renderToStaticMarkup(<InboxSourcesTable sources={[source]} manage />);
		expect(html).toContain('data-kind="inbox.source"');
		expect(html).toContain("Remove the source zz-src? Its items stay in the inbox; nothing new is pulled from it.");
		expect(html).toContain("{&quot;name&quot;:&quot;zz-src&quot;,&quot;remove&quot;:true}");
		expect(html).toContain(">Remove<");
		expect(renderToStaticMarkup(<InboxConnectAnotherLink />)).toContain("Connect another tool");
	});

	it("says nothing is connected without naming a terminal command", () => {
		const html = renderToStaticMarkup(<InboxSourcesTable sources={[]} />);
		expect(html).toContain("Nothing connected yet.");
		expect(html).not.toContain("heimdall");
	});
});

describe("InboxEmpty (wave 4, N78)", () => {
	it("keeps the command inside a block titled From a terminal", () => {
		const html = renderToStaticMarkup(<InboxEmpty />);
		expect(html).toMatch(/<details[^>]*><summary[^>]*>From a terminal<\/summary>.*heimdall inbox pull.*<\/details>/s);
	});
});