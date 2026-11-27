import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { liveMonitorProblems, MonitorAgentPicker } from "./liveMonitorsView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const ok = { name: "prod", agent: "bot", benchmark: "", tasks: "3", interval: "60", budget: "2" };

describe("live monitor form (M7-53)", () => {
	it("refuses an empty name, zero and negative numbers", () => {
		expect(liveMonitorProblems(ok, ["bot"])).toEqual([]);
		expect(liveMonitorProblems({ ...ok, name: " " }, ["bot"]).map((p) => p.key)).toEqual(["live.problem.name"]);
		expect(liveMonitorProblems({ ...ok, tasks: "0" }, ["bot"]).map((p) => p.key)).toEqual(["live.problem.tasks"]);
		expect(liveMonitorProblems({ ...ok, interval: "0" }, ["bot"]).map((p) => p.key)).toEqual(["live.problem.interval"]);
		expect(liveMonitorProblems({ ...ok, interval: "10" }, ["bot"], 15).map((p) => p.key)).toEqual(["live.problem.interval"]);
		expect(liveMonitorProblems({ ...ok, budget: "-1" }, ["bot"]).map((p) => p.key)).toEqual(["live.problem.budget"]);
		expect(liveMonitorProblems({ ...ok, budget: "" }, ["bot"]).map((p) => p.key)).toEqual(["live.problem.budget"]);
		expect(liveMonitorProblems({ ...ok, tasks: "1.5" }, ["bot"]).map((p) => p.key)).toEqual(["live.problem.tasks"]);
	});
});

describe("Monitor this agent (M7-54)", () => {
	it("never shows another agent picked for an agent with no endpoint", () => {
		const html = renderToStaticMarkup(<MonitorAgentPicker value="codex" agents={["zz-test-http"]} onChange={() => undefined} />);
		expect(html).toContain('data-testid="live-monitor-form-not-endpoint"');
		expect(html).toContain("codex has no remote endpoint");
		expect(html).toMatch(/<option value="" selected="">/);
	});
});
