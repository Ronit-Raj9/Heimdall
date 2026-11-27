import { describe, expect, it } from "vitest";
import { benchmarkAlias, isBaselineRun, runSuiteLabel, runTaskLabel, runTone } from "./runsTable";

describe("runs list (lane 5b)", () => {
	it("marks oracle and noop runs as not measured (M5-31)", () => {
		expect(isBaselineRun({ config_id: "oracle", arm: "a" })).toBe(true);
		expect(isBaselineRun({ config_id: "", arm: "noop" })).toBe(true);
		expect(isBaselineRun({ config_id: "oracle-v2", arm: "a" })).toBe(true);
		expect(isBaselineRun({ config_id: "codex", arm: "a" })).toBe(false);
		expect(isBaselineRun({ config_id: "noopsy", arm: "a" })).toBe(false);
	});
	it("gives grader and infrastructure faults the warn tone (M5-38)", () => {
		expect(runTone({ sealed: true, outcome: "invalid_evaluator" })).toBe("warn");
		expect(runTone({ sealed: true, outcome: "infra_error" })).toBe("warn");
		expect(runTone({ sealed: true, outcome: "fail" })).toBe("zero");
		expect(runTone({ sealed: true, outcome: "pass" })).toBe("pass");
		expect(runTone({ sealed: false, outcome: "fail" })).toBe("live");
	});
	it("reads ?benchmark= as the suite filter (M5-40)", () => {
		expect(benchmarkAlias({ benchmark: "hd-harbor-bfcl" })).toEqual({ suite: "hd-harbor-bfcl", benchmark: null });
		expect(benchmarkAlias({ suite: "a", benchmark: "b" })).toEqual({ suite: "a", benchmark: null });
	});
});

describe("runs list names from the server (M5-33, M5-34)", () => {
	const row = { task: "t1", suite: "hd-harbor-tb2@3" } as Parameters<typeof runTaskLabel>[0];
	it("shows task_title and suite_title when sent, else the ids", () => {
		expect(runTaskLabel({ ...row, task_title: "Fix the parser" })).toBe("Fix the parser");
		expect(runTaskLabel({ ...row, task_title: "  " })).toBe("t1");
		expect(runTaskLabel(row)).toBe("t1");
		expect(runSuiteLabel({ ...row, suite_title: "Terminal-Bench 2.0" })).toBe("Terminal-Bench 2.0");
		expect(runSuiteLabel(row)).not.toContain("@");
	});
});
