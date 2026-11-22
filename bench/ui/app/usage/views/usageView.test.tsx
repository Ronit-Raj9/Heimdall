import "@/lib/i18n";

import type { AdminUsageReport, UsageReport } from "@/lib/types/usage";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

import { AdminUsageBody, UsageBody, defaultRange, formatUSD } from "./usageView";

const report: UsageReport = {
	from: "2026-11-09",
	to: "2026-11-12",
	group_by: "day",
	total_cost_usd: 0.45,
	total_tokens: 800,
	runs: 4,
	unknown_cost_runs: 1,
	experiments: 1,
	buckets: [
		{ key: "2026-11-09", cost_usd: 0, tokens: 0, runs: 0, unknown_cost_runs: 0 },
		{ key: "2026-11-10", cost_usd: 0.35, tokens: 400, runs: 2, unknown_cost_runs: 0 },
		{ key: "2026-11-12", cost_usd: 0.1, tokens: 400, runs: 2, unknown_cost_runs: 1 },
	],
	keys: [{ id: "k1", label: "main", provider: "openai", last4: "1234", budget_usd: 1, month_spent_usd: 1.5 }],
	keys_month_spent_usd: 1.5,
};

describe("UsageBody", () => {
	it("shows totals, chart, table and keys", () => {
		const html = renderToStaticMarkup(<UsageBody data={report} />);
		expect(html).toMatch(/data-testid="usage-total-cost"[^>]*>\$0\.45</);
		expect(html).toMatch(/data-testid="usage-total-runs"[^>]*>4</);
		expect(html).toContain('data-testid="usage-unknown-note"');
		expect(html).toContain('data-testid="usage-chart"');
		expect(html).toContain('data-testid="usage-bar-2026-11-10"');
		expect(html).toContain('data-testid="usage-row-2026-11-12"');
		expect(html).toContain('data-testid="usage-key-k1"');
		expect(html).toContain("...1234");
		expect(html).not.toContain("sk-");
	});

	it("says what to do when there are no runs and no keys", () => {
		const empty = { ...report, runs: 0, total_cost_usd: 0, buckets: [], keys: [], unknown_cost_runs: 0 };
		const html = renderToStaticMarkup(<UsageBody data={empty} />);
		expect(html).toContain('data-testid="usage-empty"');
		expect(html).toContain('data-testid="usage-keys-empty"');
		expect(html).not.toContain('data-testid="usage-chart"');
	});

	it("shows the server error", () => {
		const html = renderToStaticMarkup(<UsageBody error={{ status: 400, data: { error: { message: "group_by must be one of day" } } }} />);
		expect(html).toContain('data-testid="usage-error"');
	});
});

describe("AdminUsageBody", () => {
	it("lists orgs and explains a 403", () => {
		const data: AdminUsageReport = {
			from: "2026-11-01",
			to: "2026-11-21",
			total_cost_usd: 2,
			total_tokens: 10,
			runs: 3,
			orgs: [{ org_id: "o1", slug: "acme", name: "Acme", cost_usd: 2, tokens: 10, runs: 3, unknown_cost_runs: 0, keys_month_spent_usd: 0 }],
		};
		expect(renderToStaticMarkup(<AdminUsageBody data={data} />)).toContain('data-testid="admin-usage-row-acme"');
		expect(renderToStaticMarkup(<AdminUsageBody error={{ status: 403, data: {} }} />)).toContain("HEIMDALL_SERVER_ADMINS");
	});
});

describe("helpers", () => {
	it("formats and defaults", () => {
		expect(formatUSD(1234.5)).toBe("$1,234.50");
		expect(formatUSD(0.0042)).toBe("$0.0042");
		expect(defaultRange(new Date("2026-11-21T12:00:00Z"))).toEqual({ from: "2026-10-23", to: "2026-11-21" });
	});
});

describe("UsageBody runs without a cost (F1.price)", () => {
	it("says how to price the runs left out and marks days with runs that made no model call", () => {
		const data: UsageReport = {
			...report,
			no_model_call_runs: 3,
			buckets: report.buckets.map((b) => (b.key === "2026-11-10" ? { ...b, no_model_call_runs: 3 } : b)),
		};
		const html = renderToStaticMarkup(<UsageBody data={data} />);
		expect(html).toMatch(
			/data-testid="usage-unknown-note"[^>]*>1 run\(s\) have no known cost and are left out of the total\. Set a price on the provider key in Settings &gt; API keys to include them\.</,
		);
		expect(html).toMatch(/data-testid="usage-no-call-2026-11-10"[^>]*>3 with no model call</);
		expect(html).not.toContain('data-testid="usage-no-call-2026-11-12"');
	});
});