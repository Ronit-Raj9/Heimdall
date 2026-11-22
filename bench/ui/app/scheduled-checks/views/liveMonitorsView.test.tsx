import "@/lib/i18n";

import type { MonitorRow, MonitorStats } from "@/lib/store/apis/liveMonitorsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { LineChart, samplePassRate, seriesPath } from "./liveMonitorChart";
import type { AgentSummary } from "@/lib/types/bench";
import { LiveMonitorsEmpty, MonitorAgentPicker, endpointAgents } from "./liveMonitorsView";
import { MonitorTable } from "./liveMonitorsView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const stats: MonitorStats = {
	checks: 4,
	ok_checks: 4,
	infra_checks: 0,
	passed: 9,
	scored: 12,
	pass_rate: 0.75,
	pass_low: 0.5,
	pass_high: 0.9,
	p50_ms: 1200,
	p95_ms: 3400,
	cost_usd: 0.04,
	cost_unknown: 0,
	errors: 0,
	uptime: 1,
};

const row: MonitorRow = {
	id: "m1",
	name: "prod support",
	agent: "support-bot",
	benchmark: "tau-bench",
	tasks: 3,
	interval_min: 60,
	budget_usd_per_day: 2,
	paused: false,
	rule: {},
	badge: false,
	created_at: "2026-11-22T00:00:00Z",
	updated_at: "2026-11-22T00:00:00Z",
	status: "degraded",
	schedule: "every 60 minutes",
	cron: "0 * * * *",
	check_budget_usd: 0.08,
	spent_today_usd: 0.04,
	day: stats,
	week: stats,
	open_incidents: [],
};

describe("live monitors", () => {
	it("draws a gap where a check had no graded run", () => {
		expect(seriesPath([1, null, 0.5], 1, 100, 10)).toBe("M0.0,0.0M100.0,5.0");
		expect(samplePassRate({ at: "", status: "ok", runs: [], passed: 0, scored: 0, errors: 1, cost_usd: 0 })).toBeNull();
	});

	it("renders a chart with the latest value", () => {
		const html = renderToStaticMarkup(
			<LineChart title="Pass rate" series={[{ label: "", values: [1, 0.5] }]} max={1} format={(v) => `${v * 100}%`} testId="c" />,
		);
		expect(html).toContain("50%");
		expect(html).toContain("<path");
	});

	it("lists monitors with status, pass rate and spend", () => {
		const html = renderToStaticMarkup(<MonitorTable rows={[row]} selected="m1" onSelect={() => {}} />);
		expect(html).toContain("Degraded");
		expect(html).toContain("75%");
		expect(html).toContain("1200 / 3400");
		expect(html).toContain("$0.040 / $2.00");
	});

	// J15.monitor: the empty state links to Connect, and the agent field only offers endpoint agents.
	it("links the empty state to Connect an agent", () => {
		const html = renderToStaticMarkup(<LiveMonitorsEmpty />);
		expect(html).toContain('data-testid="live-monitors-empty"');
		expect(html).toMatch(/<a[^>]*href="\/connect"[^>]*>Connect an agent<\/a>/);
	});

	it("offers only endpoint agents in the agent picker", () => {
		const agents = [
			{ id: "support-bot", owns_model: true },
			{ id: "codex", owns_model: false },
			{ id: "alpha-http", owns_model: true },
			{ id: "plain" },
		] as AgentSummary[];
		expect(endpointAgents(agents)).toEqual(["alpha-http", "support-bot"]);
		const html = renderToStaticMarkup(<MonitorAgentPicker value="support-bot" agents={endpointAgents(agents)} onChange={() => {}} />);
		expect(html).toContain('data-testid="live-monitor-form-agent"');
		expect(html).toContain('<option value="alpha-http">alpha-http</option>');
		expect(html).toContain('<option value="support-bot" selected="">support-bot</option>');
		expect(html).not.toContain("codex");
	});

	it("replaces the picker by a Connect link when no endpoint agent exists", () => {
		const html = renderToStaticMarkup(<MonitorAgentPicker value="" agents={[]} onChange={() => {}} />);
		expect(html).not.toContain("<select");
		expect(html).toContain('data-testid="live-monitor-form-connect"');
		expect(html).toMatch(/href="\/connect"/);
	});
});