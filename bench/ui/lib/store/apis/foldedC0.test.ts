import { healthArgs, listExperimentsSearch, runArgs } from "@/lib/store/apis/benchApi";
import { listEvaluationsSearch } from "@/lib/store/apis/evaluationsApi";
import { effectOf } from "@/lib/store/apis/gatewayApi";
import { describe, expect, it } from "vitest";

describe("folded shared queries (phase C lane C0)", () => {
	it("getRun takes a plain id or the failure flag", () => {
		expect(runArgs("r1")).toEqual({ runId: "r1" });
		expect(runArgs({ runId: "r1", failure: true })).toEqual({ runId: "r1", failure: true });
	});
	it("getSuiteHealth takes a plain id or tasks", () => {
		expect(healthArgs("s")).toEqual({ suite: "s" });
		expect(healthArgs({ suite: "s", tasks: ["a"] }).tasks).toEqual(["a"]);
	});
	it("listExperiments sends only set parameters", () => {
		expect(listExperimentsSearch(undefined)).toEqual({});
		expect(listExperimentsSearch({ arm_stats: true, limit: 50, offset: 0, q: "  x " })).toEqual({
			arm_stats: "1",
			limit: 50,
			offset: 0,
			q: "x",
		});
		expect(listExperimentsSearch({ arm_stats: false, q: " " })).toEqual({});
	});
	it("evaluations list joins status and keeps fields", () => {
		expect(listEvaluationsSearch({ status: ["done", "failed"], fields: "light", limit: 10 })).toEqual({
			status: "done,failed",
			fields: "light",
			limit: 10,
		});
		expect(listEvaluationsSearch(undefined)).toEqual({});
	});
	it("effectOf reads a null effect, a wrapped effect and a bare effect", () => {
		expect(effectOf({ effect: null })).toBeNull();
		expect(effectOf(null)).toBeNull();
		const e = { feature: "cache", state: "done" };
		expect(effectOf(e)).toEqual(e);
		expect(effectOf({ effect: e })).toEqual(e);
		expect(effectOf({ mode: "recorded" })).toBeNull();
	});
});