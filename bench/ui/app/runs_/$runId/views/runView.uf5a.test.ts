import { describe, expect, it } from "vitest";
import type { RunDetail } from "@/lib/types/bench";
import { gateName, orderRunTabs, rerunSearch, runCrumbs } from "./runView.uf5a";

const labels = { runs: "Runs and traces", evaluations: "Evaluations", run: "Run t08" };

describe("run page helpers (lane 5a)", () => {
	it("keeps the fixed tabs in place and puts optional tabs after Proof", () => {
		const ids = ["overview", "timeline", "recording", "output", "agents", "services", "security", "evidence"];
		expect(orderRunTabs(ids.map((id) => ({ id, label: id }))).map((t) => t.id)).toEqual([
			"overview",
			"timeline",
			"output",
			"evidence",
			"recording",
			"agents",
			"services",
			"security",
		]);
	});

	it("names crumbs, never raw ids once loaded; evaluation runs start at Evaluations", () => {
		expect(runCrumbs({ task: "t08", expId: "e_1", expName: "Nightly", labels }).map((c) => c.label)).toEqual([
			"Runs and traces",
			"Nightly",
			"Run t08",
		]);
		const ev = runCrumbs({ task: "t08", expId: "e_1", expName: "Nightly", evaluationId: "ev_1", evaluationName: "Q4", labels });
		expect(ev.map((c) => c.label)).toEqual(["Evaluations", "Q4", "Nightly", "Run t08"]);
		expect(ev[1].params).toEqual({ evalId: "ev_1" });
		expect(ev[2].search).toEqual({ from: "eval" });
	});

	it("builds the rerun search from the setup of the run's arm", () => {
		const d = { slot: { arm: "B", suite: "s", task: "t08" }, run: { agent: { config_id: "cfg" } } } as unknown as RunDetail;
		expect(rerunSearch(d, [{ arm: "B", agent: "codex", agent_file: "", model: "m1" }])).toEqual({
			suite: "s",
			task: "t08",
			step: 4,
			agent: "codex",
			model: "m1",
		});
		expect(rerunSearch(d)).toEqual({ suite: "s", task: "t08", step: 4, agent: "cfg" });
	});

	it("reads a gate's plain name, keeping an unknown id", () => {
		const t = (key: string, o: { defaultValue: string }) => (key === "gates.names.g6_null_agents" ? "An empty agent fails this task" : o.defaultValue);
		expect(gateName("g6_null_agents", t)).toBe("An empty agent fails this task");
		expect(gateName("g99", t)).toBe("g99");
	});
});
