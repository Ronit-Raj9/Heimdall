import { describe, expect, it } from "vitest";
import { benchmarkAlias, isBaselineRun, runTone } from "./runsTable";

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
