import "@/lib/i18n";

import type { AdminUsageReport, UsageReport } from "@/lib/types/usage";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

import { AdminUsageBody, UsageBody, UsageLinkNotes, defaultRange, formatUSD, rangeFromSearch, usageSearchState } from "./usageView";

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
		expect(formatUSD(1234.5)).toBe("$1,235"); // the one UI money rule (lib/money)
		expect(formatUSD(0.0042)).toBe("$0.0042");
		// This calendar month, UTC (M8-19).
		expect(defaultRange(new Date("2026-11-21T12:00:00Z"))).toEqual({ from: "2026-11-01", to: "2026-11-21" });
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
		expect(html).toMatch(/data-testid="usage-no-call-2026-11-10"[^>]*>3 runs with no model call</);
		expect(html).not.toContain('data-testid="usage-no-call-2026-11-12"');
	});

	// Fix wave 3, N184: the note sits on its own line (a block element), so the date and the count
	// never read as one number, and it names what it counts ("run" or "runs").
	it("puts the no-model-call note on its own line with a plural noun", () => {
		const one: UsageReport = {
			...report,
			buckets: report.buckets.map((b) => (b.key === "2026-11-10" ? { ...b, no_model_call_runs: 1 } : b)),
		};
		const html = renderToStaticMarkup(<UsageBody data={one} />);
		expect(html).toMatch(/<span class="[^"]*\bblock\b[^"]*" data-testid="usage-no-call-2026-11-10">1 run with no model call<\/span>/);
		const many: UsageReport = {
			...report,
			buckets: report.buckets.map((b) => (b.key === "2026-11-10" ? { ...b, no_model_call_runs: 19 } : b)),
		};
		expect(renderToStaticMarkup(<UsageBody data={many} />)).toContain(">19 runs with no model call<");
	});
});
// N75: /usage?from=... (and /settings?tab=usage&from=...) opens on the dates, grouping and scope in the link.
describe("usage link parameters", () => {
	const now = new Date("2026-11-23T12:00:00Z");
	it("takes the range from the link", () => {
		expect(rangeFromSearch({ from: "2026-11-01" }, now)).toEqual({ from: "2026-11-01", to: "2026-11-23", invalid: [] });
		expect(rangeFromSearch({ from: "2026-11-01", to: "2026-11-10" }, now)).toEqual({ from: "2026-11-01", to: "2026-11-10", invalid: [] });
		expect(rangeFromSearch({ to: "2026-10-31" }, now)).toEqual({ from: "2026-10-01", to: "2026-10-31", invalid: [] });
		expect(rangeFromSearch({}, now)).toEqual({ ...defaultRange(now), invalid: [] });
	});
	it("swaps a reversed range", () => {
		expect(rangeFromSearch({ from: "2026-11-20", to: "2026-11-01" }, now)).toEqual({
			from: "2026-11-01",
			to: "2026-11-20",
			invalid: [],
			swapped: true,
		});
	});
	it("falls back to the month of to and flags a date that is not real", () => {
		expect(rangeFromSearch({ from: "2026-02-30" }, now)).toEqual({ ...defaultRange(now), invalid: ["2026-02-30"] });
		expect(rangeFromSearch({ from: "yesterday", to: "2026-11-10" }, now)).toEqual({
			from: "2026-11-01",
			to: "2026-11-10",
			invalid: ["yesterday"],
		});
	});
	it("checks the grouping and the scope", () => {
		const st = usageSearchState({ from: "2026-11-01", group: "model", scope: "platform" }, now, true);
		expect(st).toMatchObject({ range: { from: "2026-11-01", to: "2026-11-23" }, group: "model", scope: "platform", ignored: [] });
		const bad = usageSearchState({ group: "week", scope: "platform" }, now, false);
		expect(bad.group).toBe("day");
		expect(bad.scope).toBe("org");
		expect(bad.ignored).toEqual([
			{ key: "group", value: "week" },
			{ key: "scope", value: "platform" },
		]);
	});
	it("says which link value was ignored", () => {
		const html = renderToStaticMarkup(
			<UsageLinkNotes
				ignored={[
					{ key: "from", value: "2026-02-30" },
					{ key: "group", value: "week" },
				]}
			/>,
		);
		expect(html).toContain("The date 2026-02-30 in the link is not a valid date, so the last 30 days are shown.");
		expect(html).toContain("The grouping week in the link is not one of Day, Model, Agent or Provider key, so Day is used.");
		expect(renderToStaticMarkup(<UsageLinkNotes ignored={[]} />)).toBe("");
	});
});