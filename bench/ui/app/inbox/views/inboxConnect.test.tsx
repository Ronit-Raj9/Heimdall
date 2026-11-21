import "@/lib/i18n";

import type { InboxWebhookInfo } from "@/lib/types/liveinbox";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { InboxTokensView, InboxWebhookCard, generateInboxToken } from "./inboxConnect";

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

describe("InboxTokensView (hosted)", () => {
	const list = {
		url: "https://heimdall.example/api/bench/orgs/org_a/inbox/ingest",
		max: 20,
		tokens: [{ id: "itok_1", org_id: "org_a", label: "ci", last4: "wxyz", created_at: "2026-11-21T10:00:00Z" }],
	};
	const noop = () => {};

	it("lets an admin generate and revoke, and shows a fresh token once", () => {
		const created = { token: list.tokens[0], secret: "hmi_secretvaluewxyz", url: list.url, example: "curl" };
		const html = renderToStaticMarkup(
			<InboxTokensView list={list} envName="HEIMDALL_INBOX_TOKEN" isAdmin created={created} onCreate={noop} onRevoke={noop} />,
		);
		expect(html).toContain('data-testid="inbox-webhook-generate"');
		expect(html).toContain('data-testid="inbox-token-revoke-itok_1"');
		expect(html).toContain("export HEIMDALL_INBOX_TOKEN=hmi_secretvaluewxyz");
		expect(html).toContain("hmi_...wxyz");
		expect(html).toContain("never");
	});

	it("shows a viewer the list and who to ask, with no generate or revoke", () => {
		const html = renderToStaticMarkup(
			<InboxTokensView list={list} envName="HEIMDALL_INBOX_TOKEN" isAdmin={false} created={null} onCreate={noop} onRevoke={noop} />,
		);
		expect(html).toContain('data-testid="inbox-tokens-viewer"');
		expect(html).toContain('data-testid="inbox-tokens-table"');
		expect(html).not.toContain('data-testid="inbox-webhook-generate"');
		expect(html).not.toContain("inbox-token-revoke");
	});

	it("offers the next step when there are no tokens", () => {
		const html = renderToStaticMarkup(
			<InboxTokensView list={{ ...list, tokens: [] }} envName="X" isAdmin created={null} onCreate={noop} onRevoke={noop} />,
		);
		expect(html).toContain('data-testid="inbox-tokens-empty"');
		expect(html).toContain("Generate one");
	});
});

describe("generateInboxToken", () => {
	it("gives 64 hex characters", () => {
		expect(generateInboxToken((a) => a.fill(171))).toBe("ab".repeat(32));
		expect(generateInboxToken()).toMatch(/^[0-9a-f]{64}$/);
	});
});
