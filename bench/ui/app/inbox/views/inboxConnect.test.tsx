import "@/lib/i18n";

import type { InboxWebhookInfo } from "@/lib/types/liveinbox";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { InboxWebhookCard, generateInboxToken } from "./inboxConnect";

const base: InboxWebhookInfo = {
	url: "http://localhost:8090/api/bench/inbox/ingest",
	enabled: false,
	configured: false,
	token_env: "HEIMDALL_INBOX_TOKEN",
	token_set: false,
	hosted: false,
	next: "add the webhook source below to inbox/sources.yaml",
	example: 'curl -X POST http://localhost:8090/api/bench/inbox/ingest -H "Authorization: Bearer $HEIMDALL_INBOX_TOKEN"',
	sources_snippet: "sources:\n  - name: webhook\n    kind: webhook\n    token_env: HEIMDALL_INBOX_TOKEN\n",
};

describe("InboxWebhookCard", () => {
	it("shows the ingest URL, the next step, the snippet and the generate button while off", () => {
		const html = renderToStaticMarkup(<InboxWebhookCard info={base} />);
		expect(html).toContain('data-testid="inbox-webhook-url"');
		expect(html).toContain("http://localhost:8090/api/bench/inbox/ingest");
		expect(html).toContain('data-testid="inbox-webhook-off"');
		expect(html).toContain('data-testid="inbox-webhook-next"');
		expect(html).toContain('data-testid="inbox-webhook-snippet"');
		expect(html).toContain('data-testid="inbox-webhook-generate"');
		expect(html).toContain("not set on the server");
	});

	it("hides the generate button once on", () => {
		const html = renderToStaticMarkup(<InboxWebhookCard info={{ ...base, enabled: true, configured: true, token_set: true, next: undefined, sources_snippet: undefined }} />);
		expect(html).toContain('data-testid="inbox-webhook-on"');
		expect(html).not.toContain('data-testid="inbox-webhook-generate"');
		expect(html).toContain("set on the server");
	});

	it("explains the hosted refusal", () => {
		const html = renderToStaticMarkup(<InboxWebhookCard info={{ ...base, hosted: true, problem: "the inbox webhook is not served on the hosted server yet" }} />);
		expect(html).toContain('data-testid="inbox-webhook-problem"');
		expect(html).not.toContain('data-testid="inbox-webhook-generate"');
	});
});

describe("generateInboxToken", () => {
	it("gives 64 hex characters", () => {
		expect(generateInboxToken((a) => a.fill(171))).toBe("ab".repeat(32));
		expect(generateInboxToken()).toMatch(/^[0-9a-f]{64}$/);
	});
});
