import "@/lib/i18n";

import type { RunAgents } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentsPanel, RunAgentsPanel } from "./agentsSection";

const query = vi.hoisted(() => ({ state: {} as Record<string, unknown>, args: [] as unknown[] }));

vi.mock("@/lib/store/apis/runAgentsApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/runAgentsApi")>();
	return {
		...actual,
		useGetRunAgentsQuery: (arg: unknown) => {
			query.args.push(arg);
			return query.state;
		},
	};
});

const FIXTURES = path.resolve(__dirname, "../../../public/fixtures");
const read = (file: string) => JSON.parse(fs.readFileSync(path.join(FIXTURES, file), "utf8")) as RunAgents;
const team = read("runs.r_fmw1z7ouim.agents.json");
const single = read("runs.r_pww2l7yame.agents.json");

const idle = { isLoading: false, isFetching: false, error: undefined, data: undefined, refetch: () => undefined };

beforeEach(() => {
	query.state = idle;
	query.args = [];
});

describe("RunAgentsPanel", () => {
	it("shows a skeleton while loading", () => {
		query.state = { ...idle, isLoading: true };
		const html = renderToStaticMarkup(<RunAgentsPanel runId="r_1" arm="A" />);
		expect(html).toContain('data-testid="agents-A-loading"');
		expect(query.args).toEqual(["r_1"]);
	});

	it("shows the error with a retry", () => {
		query.state = { ...idle, error: { status: 500, data: { error: "boom" } } };
		const html = renderToStaticMarkup(<RunAgentsPanel runId="r_1" arm="B" />);
		expect(html).toContain('data-testid="agents-B-error"');
		expect(html).toContain("Could not load the run&#x27;s agents.");
		expect(html).toContain('data-testid="agents-B-error-retry"');
	});

	it("renders the data once loaded", () => {
		query.state = { ...idle, data: team };
		const html = renderToStaticMarkup(<RunAgentsPanel runId="r_fmw1z7ouim" arm="A" />);
		expect(html).toContain('data-testid="agents-A"');
	});
});

describe("AgentsPanel", () => {
	it("says a single-agent run has nothing to show", () => {
		const html = renderToStaticMarkup(<AgentsPanel data={single} arm="B" />);
		expect(html).toContain('data-testid="agents-B-unavailable"');
		expect(html).toContain("Single-agent run");
		expect(html).not.toContain('data-testid="agents-blame-B"');
	});

	it("renders the team fixture: shape, coverage, blame, swimlanes, tree, table and handoffs", () => {
		const html = renderToStaticMarkup(<AgentsPanel data={team} arm="A" />);
		expect(html).toContain("team: one container per member");
		expect(html).toContain("4 agents");
		// 22 of 24 steps attributed: under 95%, a warning.
		expect(html).toMatch(/data-testid="agents-A-coverage" data-tone="warn"/);
		expect(html).toContain('data-testid="agents-blame-A" data-tier="gate_proven"');
		expect(html).toContain("LLM-judge blame off. Best step accuracy is 14%.");
		expect(html).toContain('data-testid="agents-swimlane-A"');
		expect(html.match(/data-testid="agents-handoff-marker-A-\d+"/g)).toHaveLength(
			team.handoffs.filter((h) => h.at_ms !== null && h.to).length,
		);
		expect(html).toContain('data-testid="agents-tree-A"');
		expect(html).toContain('data-testid="agents-table-A"');
		expect(html).toContain('data-testid="agents-handoffs-A"');
		expect(html).toContain('data-testid="agents-A-notes"');
		expect(html).not.toContain('data-testid="agents-A-truncated"');
	});

	it("says when the timeline was truncated and when no blame was recorded", () => {
		const html = renderToStaticMarkup(<AgentsPanel data={{ ...team, truncated: true, blame: undefined }} arm="A" />);
		expect(html).toContain('data-testid="agents-A-truncated"');
		expect(html).toContain('data-testid="agents-A-no-blame"');
	});

	it("falls back to the blame's coverage and says when there is none", () => {
		const withBlameCoverage = renderToStaticMarkup(<AgentsPanel data={{ ...team, tree: undefined }} arm="A" />);
		expect(withBlameCoverage).toContain('data-testid="agents-A-coverage"');
		expect(withBlameCoverage).toContain("shape not recorded");
		const none = renderToStaticMarkup(<AgentsPanel data={{ ...team, tree: undefined, blame: undefined }} arm="A" />);
		expect(none).toContain("Attribution coverage not recorded");
	});
});

describe("team fixture", () => {
	it("is internally consistent", () => {
		const busy = team.members.reduce((sum, m) => sum + m.busy_ms, 0);
		expect(team.busy_ms).toBe(busy);
		for (const m of team.members) {
			const spans = team.spans.filter((s) => s.agent_id === m.agent_id);
			expect(spans.length, m.agent_id).toBe(m.steps);
			expect(
				spans.reduce((sum, s) => sum + s.end_ms - s.start_ms, 0),
				m.agent_id,
			).toBe(m.busy_ms);
		}
		const critical = team.spans.filter((s) => s.critical).reduce((sum, s) => sum + s.end_ms - s.start_ms, 0);
		expect(team.critical_ms).toBe(critical);
		expect(team.tree?.coverage.attributed).toBe(team.spans.length);
		expect(team.blame?.agent_id).toBe("worker-b");
		expect(team.handoffs.some((h) => h.altered && h.facts?.some((f) => f.sent && !f.delivered))).toBe(true);
		for (const h of team.handoffs) {
			expect(["gateway_key", "self_report"]).toContain(h.source);
			expect(h.at_ms === null || (h.at_ms >= 0 && h.at_ms <= team.wall_ms)).toBe(true);
		}
	});
});