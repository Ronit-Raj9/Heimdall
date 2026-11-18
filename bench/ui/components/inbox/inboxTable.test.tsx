import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { INBOX_LIST_FIXTURE } from "./inbox.fixtures";
import { InboxStateFilter } from "./inboxStateFilter";
import { InboxTable } from "./inboxTable";

const noop = () => {};

/** The text inside the element with the test id. */
function textOf(html: string, testId: string): string {
	const match = html.match(new RegExp(`data-testid="${testId}"[^>]*>(.*?)</`));
	expect(match, testId).not.toBeNull();
	return match![1];
}

describe("InboxStateFilter", () => {
	it("shows the count of every state and of all items", () => {
		const html = renderToStaticMarkup(<InboxStateFilter counts={INBOX_LIST_FIXTURE.counts} value="new" onChange={noop} />);
		expect(textOf(html, "inbox-count-all")).toBe("6");
		expect(textOf(html, "inbox-count-new")).toBe("1");
		expect(textOf(html, "inbox-count-accepted")).toBe("2");
		expect(textOf(html, "inbox-count-rejected")).toBe("1");
		expect(textOf(html, "inbox-count-duplicate")).toBe("1");
		expect(textOf(html, "inbox-count-snoozed")).toBe("1");
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="inbox-state-new"/);
		expect(html).toMatch(/aria-pressed="false"[^>]*data-testid="inbox-state-all"/);
	});

	it("reads a missing state as zero", () => {
		const html = renderToStaticMarkup(<InboxStateFilter counts={{ new: 4 }} value="all" onChange={noop} />);
		expect(textOf(html, "inbox-count-all")).toBe("4");
		expect(textOf(html, "inbox-count-snoozed")).toBe("0");
	});
});

describe("InboxTable", () => {
	const html = renderToStaticMarkup(<InboxTable items={INBOX_LIST_FIXTURE.items} selectedId="inc-3f9a1c2b7d10" onSelect={noop} />);

	it("lists every item with its title, source, flagged reasons, occurrences and state", () => {
		for (const item of INBOX_LIST_FIXTURE.items) {
			expect(html).toContain(`data-testid="inbox-row-${item.id}"`);
			expect(textOf(html, `inbox-occurrences-${item.id}`)).toBe(String(item.occurrences));
		}
		expect(html).toContain("Please refund order A-&lt;n&gt;, the tent arrived torn.");
		expect(html).toContain("prod-langfuse");
		expect(html).toContain("score helpfulness = 0 (below 0.5)");
		expect(textOf(html, "inbox-state-badge-inc-3f9a1c2b7d10")).toBe("New");
		expect(textOf(html, "inbox-state-badge-inc-9c11aa02f3b4")).toBe("Snoozed");
		expect(html).toMatch(/aria-selected="true"[^>]*data-testid="inbox-row-inc-3f9a1c2b7d10"/);
	});

	it("shows the suggested kind and suite", () => {
		expect(html).toMatch(/data-testid="inbox-suggested-inc-3f9a1c2b7d10"[^>]*><div[^>]*><span>MCP<\/span><span[^>]*>support<\/span>/);
		expect(html).toMatch(/data-testid="inbox-suggested-inc-9c11aa02f3b4"[^>]*><div[^>]*><span>Memory<\/span><span[^>]*>no suite<\/span>/);
	});

	it("links the original trace only when the item has a link", () => {
		expect(html).toContain('href="https://cloud.langfuse.com/project/p1/traces/abc123"');
		expect(html).toContain('data-testid="inbox-trace-link-inc-3f9a1c2b7d10"');
		expect(html).toContain('target="_blank"');
		expect(html).toContain('rel="noopener noreferrer"');
		expect(html).not.toContain('data-testid="inbox-trace-link-inc-7b20c4d9e811"');
	});

	it("never renders a link that is not http or https", () => {
		const item = { ...INBOX_LIST_FIXTURE.items[0], link: "javascript:alert(1)" };
		const out = renderToStaticMarkup(<InboxTable items={[item]} onSelect={noop} />);
		expect(out).not.toContain("javascript:");
		expect(out).not.toContain("inbox-trace-link-");
	});

	it("says what an accepted item became and how its task does", () => {
		expect(html).toContain("accepted as support/warranty-period");
		expect(textOf(html, "inbox-release-inc-7b20c4d9e811")).toBe("passes in e_123 (3/3)");
		expect(textOf(html, "inbox-recurrences-inc-7b20c4d9e811")).toBe("recurred 1 time since accepted");
		expect(html).toContain("accepted as support/address-change");
		expect(textOf(html, "inbox-release-inc-2a3b4c5d6e7f")).toBe("not run yet");
		expect(html).not.toContain("inbox-recurrences-inc-2a3b4c5d6e7f");
	});

	it("says fails when the task fails", () => {
		const accepted = INBOX_LIST_FIXTURE.items.find((i) => i.id === "inc-7b20c4d9e811")!;
		const item = { ...accepted, release: { status: "fails" as const, experiment: "e_200", passed: 1, runs: 3 } };
		const out = renderToStaticMarkup(<InboxTable items={[item]} onSelect={noop} />);
		expect(textOf(out, "inbox-release-inc-7b20c4d9e811")).toBe("fails in e_200 (1/3)");
		expect(out).toContain('data-status="fails"');
	});

	it("shows two flagged reasons and counts the rest", () => {
		const item = { ...INBOX_LIST_FIXTURE.items[0], flagged: ["a", "b", "c", "d"] };
		const out = renderToStaticMarkup(<InboxTable items={[item]} onSelect={noop} />);
		expect(out).toContain("+2 more");
		expect(out).not.toContain(">c<");
	});
});