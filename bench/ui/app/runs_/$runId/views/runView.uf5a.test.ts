import { describe, expect, it } from "vitest";
import type { RunDetail } from "@/lib/types/bench";
import { gateName, gradeAfterCause, orderRunTabs, rerunSearch, runCrumbs } from "./runView.uf5a";
import { runsSearchFrom } from "@/components/runs/runsListSearch";
import uf5a from "@/locales/en/uf5a.json";
import uf3 from "@/locales/en/uf3.json";

const labels = { runs: "Runs", evaluations: "Evaluations", run: "Run t08" };

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
			"Runs",
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

	it("keeps the runs list filters on the Runs crumb (M5-7)", () => {
		const search = runsSearchFrom("?outcome=fail&suite=s1&x=1");
		expect(search).toEqual({ outcome: "fail", suite: "s1" });
		const crumbs = runCrumbs({ task: "t08", expId: "e_1", labels, runsSearch: search });
		expect(crumbs[0]).toEqual({ label: "Runs", to: "/runs", search: { outcome: "fail", suite: "s1" } });
		expect(runsSearchFrom("?x=1")).toBeUndefined();
		expect(runCrumbs({ task: "t08", expId: "e_1", labels })[0].search).toBeUndefined();
	});

	it("explains the grader's reason by the tool server cause, one story per page (R3-2)", () => {
		const grade = { reason: "test run broken by the patch: no reward file (exit 3)." };
		const explain = (r: string) => `${r}. follows from the tool error`;
		expect(gradeAfterCause(grade, true, explain)?.reason).toBe("test run broken by the patch: no reward file (exit 3). follows from the tool error");
		expect(gradeAfterCause(grade, false, explain)).toBe(grade);
		expect(gradeAfterCause({ reason: "" }, true, explain)?.reason).toBe("");
		expect(gradeAfterCause(null, true, explain)).toBeUndefined();
		expect(uf5a.claim.afterTool).toContain("tool server error above");
	});

	it("names the last gate and says the rerun link runs the benchmark, like the wizard banner (F3-16, R3-3)", () => {
		expect(uf5a.gates.names.insecure_code_added).toBe("Did not add insecure code");
		expect(uf5a.page.rerun).toBe("Run this benchmark again (1 task)");
		expect(uf5a.page.rerunHint).toContain("can be a different task of the same benchmark");
		expect(uf3.link.task).toContain("it can be a different task than {{task}}");
		expect(uf5a.page.crumbRuns).toBe("Runs");
	});
});
