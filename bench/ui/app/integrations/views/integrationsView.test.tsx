import "@/lib/i18n";

import { parseRepos } from "@/lib/store/apis/integrationsApi";
import type { Webhook } from "@/lib/types/integrations";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ExportTable, WebhookList, exportParams } from "./integrationsView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const hook: Webhook = {
	id: "wh_1",
	url: "https://example.com/hook",
	events: ["experiment.finished"],
	enabled: true,
	secret_last4: "abcd",
	created_at: "2026-11-21T00:00:00Z",
	last_delivery: { id: "d1", hook_id: "wh_1", event: "experiment.finished", event_id: "e1", at: "2026-11-21T00:00:00Z", ok: false, status_code: 500, error: "receiver answered HTTP 500", attempts: 3, duration_ms: 10 },
};

describe("integrations view", () => {
	it("lists webhooks with their last delivery and a test result", () => {
		const html = renderToStaticMarkup(
			<WebhookList hooks={[hook]} tests={{ wh_1: { ...hook.last_delivery!, ok: true, status_code: 204 } }} onTest={() => {}} onToggle={() => {}} onDelete={() => {}} />,
		);
		expect(html).toContain('data-testid="webhook-list"');
		expect(html).toContain("https://example.com/hook");
		expect(html).toContain("last delivery failed");
		expect(html).toContain("whsec_...abcd");
		expect(html).toContain("Test delivered (HTTP 204)");
	});

	it("offers a next step when there is no webhook or export", () => {
		expect(renderToStaticMarkup(<WebhookList hooks={[]} onTest={() => {}} onToggle={() => {}} onDelete={() => {}} />)).toContain("webhooks-empty");
		expect(renderToStaticMarkup(<ExportTable exports={[]} onDelete={() => {}} />)).toContain("exports-empty");
	});

	it("links each export file for download", () => {
		const html = renderToStaticMarkup(
			<ExportTable exports={[{ name: "exp1-20261121-000000.otlp.json", size: 2048, created_at: "2026-11-21T00:00:00Z", format: "otlp" }]} onDelete={() => {}} />,
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

	it("reads GitHub repos in either shape", () => {
		expect(parseRepos([{ full_name: "a/b" }, { owner: { login: "c" }, name: "d" }, { name: "x" }])).toEqual([
			{ full_name: "a/b", default_branch: undefined },
			{ full_name: "c/d", default_branch: undefined },
		]);
		expect(parseRepos({ repos: [{ owner: "e", name: "f", default_branch: "main" }] })).toEqual([{ full_name: "e/f", default_branch: "main" }]);
	});
});
