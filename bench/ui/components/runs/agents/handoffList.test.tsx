import "@/lib/i18n";

import type { AgentHandoff } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { HandoffList } from "./handoffList";

const lossy: AgentHandoff = {
	index: 1,
	kind: "message",
	from: "lead",
	to: "worker-b",
	at: "2026-11-14T18:41:14.400Z",
	at_ms: 9400,
	bytes: 1400,
	payload_sha256: "sha256:52b5454d00000000000000000000000000000000000000000000000000009338",
	sent: "Rewrite the totals query.\nNever edit files under migrations/\nRun the tests.",
	delivered: "Rewrite the totals query.\nRun the tests.",
	altered: true,
	ref: "tool_call:call_2",
	source: "gateway_key",
	facts: [
		{ fact: "Invoice totals round half-even.", sent: true, delivered: true },
		{ fact: "Never edit files under migrations/", sent: true, delivered: false },
	],
};
const unrecorded: AgentHandoff = { index: 2, kind: "message", from: "lead", to: "reviewer", at_ms: null, bytes: 58, source: "gateway_key" };
const same: AgentHandoff = {
	index: 3,
	kind: "return",
	from: "worker-a",
	to: "lead",
	at_ms: 97000,
	bytes: 12,
	sent: "<script>alert(1)</script>",
	delivered: "<script>alert(1)</script>",
	source: "zz_custom",
};
const claim: AgentHandoff = { index: 4, kind: "claim", from: "worker-a", at_ms: 1200, bytes: 0, source: "self_report", sent: "task 3" };

describe("HandoffList", () => {
	it("shows the source as a strength badge: proven for a gateway key, reported otherwise", () => {
		const html = renderToStaticMarkup(<HandoffList handoffs={[lossy, claim, same]} arm="A" />);
		expect(html).toMatch(/data-testid="agents-handoff-A-1-source" data-tone="good"[^>]*>.*?proven.*?via gateway key/);
		expect(html).toMatch(/data-testid="agents-handoff-A-4-source" data-tone="warn"[^>]*>.*?reported.*?via self-report/);
		expect(html).toMatch(/data-testid="agents-handoff-A-3-source" data-tone="warn"[^>]*>.*?via zz_custom/);
	});

	it("lists kind, from and to with an icon, time, size, short hash and the altered badge", () => {
		const html = renderToStaticMarkup(<HandoffList handoffs={[lossy, unrecorded]} arm="A" />);
		expect(html).toContain('data-testid="agents-handoff-A-1" data-altered="true"');
		expect(html).toContain("lucide-arrow-right");
		expect(html).toContain('<span class="sr-only">to</span>');
		expect(html).toContain("18:41:14.400");
		expect(html).toContain("1.4 kB");
		expect(html).toContain("52b5454d…9338");
		expect(html).toContain('data-testid="agents-handoff-A-1-altered"');
		expect(html).toContain("time not recorded");
		expect(html).not.toContain('data-testid="agents-handoff-A-1-panel"');
		expect(html).toContain('aria-expanded="false"');
	});

	it("marks a lost fact as a warning and a kept one as good", () => {
		const html = renderToStaticMarkup(<HandoffList handoffs={[lossy]} arm="A" />);
		const kept = html.match(/data-testid="agents-handoff-A-1-facts-0"[^>]*>.*?<\/li>/)?.[0] ?? "";
		const lost = html.match(/data-testid="agents-handoff-A-1-facts-1"[^>]*>.*?<\/li>/)?.[0] ?? "";
		expect(kept).toContain('data-state="kept"');
		expect(kept).toContain('data-tone="good"');
		expect(lost).toContain('data-state="lost"');
		expect(lost).toContain('data-tone="warn"');
		expect(lost).toContain("lost");
		expect(lost).toContain("not delivered");
	});

	it("opens to a line diff of sent against delivered", () => {
		const html = renderToStaticMarkup(<HandoffList handoffs={[lossy]} arm="A" initialExpanded={[1]} />);
		expect(html).toContain('aria-expanded="true"');
		expect(html).toContain('data-testid="agents-handoff-A-1-payload-diff"');
		expect(html).toMatch(/class="[^"]*bg-chart-error\/10[^"]*" data-op="del">.*?Never edit files under migrations\//);
		expect(html.match(/data-op="del"/g)).toHaveLength(1);
		expect(html).not.toContain('data-op="add"');
		expect(html).toContain("tool_call:call_2");
	});

	it("says when the payload was not recorded, or only one side was", () => {
		const html = renderToStaticMarkup(<HandoffList handoffs={[unrecorded, claim]} arm="A" initialExpanded={[2, 4]} />);
		expect(html).toContain('data-testid="agents-handoff-A-2-payload-none"');
		expect(html).toContain("Payload not recorded");
		expect(html).toContain('data-testid="agents-handoff-A-4-payload-one"');
		expect(html).toContain("Only the payload as sent was recorded:");
		expect(html).toContain("no recipient");
	});

	it("shows a matching payload once, inert", () => {
		const html = renderToStaticMarkup(<HandoffList handoffs={[same]} arm="A" initialExpanded={[3]} />);
		expect(html).toContain('data-testid="agents-handoff-A-3-payload-same"');
		expect(html).toContain("Delivered as sent:");
		expect(html).not.toContain("<script>");
		expect(html).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
		expect(html).toContain("zz_custom");
	});

	it("says when there are no handoffs", () => {
		expect(renderToStaticMarkup(<HandoffList handoffs={[]} arm="B" />)).toContain('data-testid="agents-handoffs-B-empty"');
	});
});