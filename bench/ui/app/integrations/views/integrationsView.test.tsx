import "@/lib/i18n";

import type { Webhook } from "@/lib/types/integrations";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import {
	CIInstallLink,
	DeliveryTable,
	ExportTable,
	WebhookList,
	WebhookUrlField,
	anyRetrying,
	ciNextStep,
	defaultEventsFor,
	eventsFor,
	exportParams,
	webhookFormReady,
	webhookUrlProblem,
} from "./integrationsView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const hook: Webhook = {
	id: "wh_1",
	url: "https://example.com/hook",
	events: ["experiment.finished"],
	enabled: true,
	secret_last4: "abcd",
	created_at: "2026-11-21T00:00:00Z",
	last_delivery: {
		id: "d1",
		hook_id: "wh_1",
		event: "experiment.finished",
		event_id: "e1",
		at: "2026-11-21T00:00:00Z",
		ok: false,
		status_code: 500,
		error: "receiver answered HTTP 500",
		attempts: 3,
		duration_ms: 10,
	},
};

describe("integrations view", () => {
	it("lists webhooks with their last delivery and a test result", () => {
		const html = renderToStaticMarkup(
			<WebhookList
				hooks={[hook]}
				tests={{ wh_1: { ...hook.last_delivery!, ok: true, status_code: 204 } }}
				onTest={() => {}}
				onToggle={() => {}}
				onDelete={() => {}}
			/>,
		);
		expect(html).toContain('data-testid="webhook-list"');
		expect(html).toContain("https://example.com/hook");
		expect(html).toContain("last delivery failed");
		expect(html).toContain("whsec_...abcd");
		expect(html).toContain("Test delivered (HTTP 204)");
	});

	it("shows a chat or paging hook by its service, without a signing secret", () => {
		const slack: Webhook = {
			...hook,
			id: "wh_2",
			format: "slack",
			events: ["evaluation.finished", "evaluation.regressed"],
			last_delivery: undefined,
		};
		const pd: Webhook = {
			...hook,
			id: "wh_3",
			format: "pagerduty",
			events: ["evaluation.regressed"],
			routing_key_last4: "9f2c",
			last_delivery: undefined,
		};
		const html = renderToStaticMarkup(<WebhookList hooks={[slack, pd]} onTest={() => {}} onToggle={() => {}} onDelete={() => {}} />);
		expect(html).toContain("Slack");
		expect(html).toContain("PagerDuty");
		expect(html).toContain("An evaluation finished, An agent got worse");
		expect(html).toContain("key ...9f2c");
		expect(html).not.toContain("whsec_...");
	});

	it("limits PagerDuty to problems and checks what each format needs", () => {
		expect(eventsFor("pagerduty")).not.toContain("evaluation.finished");
		expect(eventsFor("slack")).toContain("evaluation.finished");
		expect(defaultEventsFor("pagerduty")).toEqual(["evaluation.regressed"]);
		expect(defaultEventsFor("teams")).toContain("release.blocked");
		expect(webhookFormReady("pagerduty", "", "a".repeat(32), ["evaluation.regressed"])).toBe(true);
		expect(webhookFormReady("pagerduty", "", "short", ["evaluation.regressed"])).toBe(false);
		expect(webhookFormReady("slack", "", "", ["evaluation.finished"])).toBe(false);
		expect(webhookFormReady("slack", "https://hooks.slack.com/services/x", "", [])).toBe(false);
		expect(webhookFormReady("slack", "https://hooks.slack.com/services/x", "", ["evaluation.finished"])).toBe(true);
	});

	it("offers a next step when there is no webhook or export", () => {
		expect(renderToStaticMarkup(<WebhookList hooks={[]} onTest={() => {}} onToggle={() => {}} onDelete={() => {}} />)).toContain(
			"webhooks-empty",
		);
		expect(renderToStaticMarkup(<ExportTable exports={[]} onDelete={() => {}} />)).toContain("exports-empty");
	});

	it("links each export file for download", () => {
		const html = renderToStaticMarkup(
			<ExportTable
				exports={[{ name: "exp1-20261121-000000.otlp.json", size: 2048, created_at: "2026-11-21T00:00:00Z", format: "otlp" }]}
				onDelete={() => {}}
			/>,
		);
		expect(html).toContain('href="/api/bench/integrations/exports/exp1-20261121-000000.otlp.json"');
		expect(html).toContain("2.0 KiB");
	});

	it("builds export job params per format", () => {
		expect(exportParams("otlp", "", "", false, false)).toBeNull();
		expect(exportParams("otlp", "e1", "", true, true)).toEqual({ format: "otlp", exp_id: "e1", payloads: true });
		expect(exportParams("eee", "e1", "", true, true)).toEqual({ format: "eee", exp_id: "e1", allow_synthetic: true });
		expect(exportParams("harbor", "e1", "s1", false, false)).toEqual({ format: "harbor", suite: "s1" });
	});

	it("sends CI install to the one Settings > GitHub flow", () => {
		expect(ciNextStep(undefined, true)).toBe("manual");
		expect(ciNextStep({ configured: false, connected: false }, false)).toBe("manual");
		expect(ciNextStep({ configured: true, connected: false }, false)).toBe("connect");
		expect(ciNextStep({ configured: true, connected: true, login: "me" }, false)).toBe("install");
		const install = renderToStaticMarkup(<CIInstallLink step="install" />);
		expect(install).toContain("ci-install-link");
		expect(install).toContain("Open Settings &gt; GitHub");
		expect(renderToStaticMarkup(<CIInstallLink step="connect" />)).toContain("ci-not-connected");
		expect(renderToStaticMarkup(<CIInstallLink step="manual" />)).toContain("by hand");
	});
});

describe("delivery log (X16)", () => {
	const base = { hook_id: "wh_1", event: "experiment.finished", event_id: "e1", at: "2026-11-21T00:00:00Z", duration_ms: 5 };
	const rows = [
		{
			...base,
			id: "d3",
			ok: false,
			attempts: 2,
			status_code: 503,
			error: "receiver answered HTTP 503",
			state: "retrying" as const,
			next_attempt_at: "2026-11-21T00:05:00Z",
		},
		{ ...base, id: "d2", ok: false, attempts: 9, error: "connection refused", state: "failed" as const },
		{ ...base, id: "d1", ok: true, attempts: 1, status_code: 204, state: "delivered" as const },
	];

	it("shows state badges, status, attempts and Retry only on failed rows for members", () => {
		const html = renderToStaticMarkup(
			<DeliveryTable deliveries={rows} hookUrls={{ wh_1: "https://example.com/hook" }} canRetry onRetry={() => {}} />,
		);
		expect(html).toContain('data-testid="delivery-table"');
		expect(html).toContain("Retrying");
		expect(html).toContain("Failed");
		expect(html).toContain("Delivered");
		expect(html).toContain("503");
		expect(html).toContain("connection refused");
		expect(html).toContain("https://example.com/hook");
		expect(html).toContain('data-testid="delivery-retry-d2"');
		expect(html).not.toContain("delivery-retry-d3");
		expect(html).not.toContain("delivery-retry-d1");
		expect(renderToStaticMarkup(<DeliveryTable deliveries={rows} canRetry={false} onRetry={() => {}} />)).not.toContain("delivery-retry-");
	});

	it("refreshes only while a row is retrying and says when there is nothing yet", () => {
		expect(anyRetrying(rows)).toBe(true);
		expect(anyRetrying(rows.slice(1))).toBe(false);
		expect(renderToStaticMarkup(<DeliveryTable deliveries={[]} canRetry onRetry={() => {}} />)).toContain("deliveries-empty");
	});
});

// F6.bad: a hosted server delivers only to https; the form says so under the field.
describe("webhook address check", () => {
	it("refuses an http address on the hosted site, with a hint under the field", () => {
		expect(webhookUrlProblem("http://example.com/hook", true)).toBe("httpsOnly");
		expect(webhookUrlProblem(" HTTPS://example.com/hook", true)).toBeNull();
		expect(webhookUrlProblem("http://localhost:9000/hook", false)).toBeNull();
		expect(webhookUrlProblem("", true)).toBeNull();
		const html = renderToStaticMarkup(<WebhookUrlField format="json" url="http://example.com/hook" hosted onChange={() => {}} />);
		expect(html).toContain('data-testid="webhook-url-problem"');
		expect(html).toContain("Only https addresses are allowed on this server.");
		expect(html).toContain('aria-invalid="true"');
		const ok = renderToStaticMarkup(<WebhookUrlField format="json" url="https://example.com/hook" hosted onChange={() => {}} />);
		expect(ok).not.toContain("webhook-url-problem");
	});
	it("keeps Add web address disabled for an http address on the hosted site", () => {
		expect(webhookFormReady("json", "http://example.com/hook", "", ["experiment.finished"], true)).toBe(false);
		expect(webhookFormReady("json", "https://example.com/hook", "", ["experiment.finished"], true)).toBe(true);
		expect(webhookFormReady("json", "http://example.com/hook", "", ["experiment.finished"])).toBe(true);
	});
});