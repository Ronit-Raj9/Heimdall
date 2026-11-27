import "@/lib/i18n";

import type { AgentCompatibility, Estimate } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it } from "vitest";
import { FAILURE_FAULTS, faultsNotApplicable, isDefaultFaults, launchConfirm, plannedRuns, visibleSuites } from "./failureTestsView";
import { ftFaultName } from "./gw07I18n";

describe("failure test launch (M5-44, M2-111)", () => {
	it("counts every task once clean and once per fault, per repeat and benchmark", () => {
		expect(plannedRuns({ benchmarks: ["a", "b"], faults: ["model-down", "broken-tool", "clean"], tasks: 3, trials: 2 })).toBe(36);
		expect(plannedRuns({ benchmarks: [], faults: ["model-down"], tasks: 3, trials: 1 })).toBe(0);
	});
	it("asks with the high cost when the price is known", () => {
		const e = { runs: 12, est_cost_usd: { low: 0.4, high: 0.9, known: true } } as Estimate;
		expect(launchConfirm(e, 99)).toEqual({ key: "ft.confirmKnown", values: { runs: 12, cost: "$0.90" } });
	});
	it("falls back to the typed cap, then the server cap, then price not known", () => {
		const unknown = { runs: 6, est_cost_usd: { low: 0, high: 0, known: false } } as Estimate;
		expect(launchConfirm(unknown, 6, "2.5")).toEqual({ key: "ft.confirmCap", values: { runs: 6, cap: "$2.50" } });
		const capped = { ...unknown, budget: { budget_usd: 10 } } as Estimate;
		expect(launchConfirm(capped, 6)).toEqual({ key: "ft.confirmCap", values: { runs: 6, cap: "$10.00" } });
		expect(launchConfirm(undefined, 8)).toEqual({ key: "ft.confirmUnknown", values: { runs: 8 } });
	});
});

describe("failure test form helpers", () => {
	it("knows the opening fault set", () => {
		expect(isDefaultFaults([...FAILURE_FAULTS].reverse())).toBe(true);
		expect(isDefaultFaults(["model-down"])).toBe(false);
	});
	it("greys out only faults no picked benchmark takes", () => {
		const e = {
			per_benchmark: [
				{ benchmark: "a", faults_skipped: [{ fault: "broken-tool", reason: "" }, { fault: "model-down", reason: "" }] },
				{ benchmark: "b", faults_skipped: [{ fault: "broken-tool", reason: "" }] },
			],
		} as unknown as Estimate;
		expect([...faultsNotApplicable(e)]).toEqual(["broken-tool"]);
		expect(faultsNotApplicable(undefined).size).toBe(0);
	});
	it("filters benchmarks by search and runnable, keeping picked ones", () => {
		const suites = [
			{ id: "hd-harbor-bfcl", description: "function calling" },
			{ id: "longmemeval-s", description: "memory" },
		];
		const compat = { agent: "x", benchmarks: [{ id: "longmemeval-s", ok: false, reason: "needs sessions" }] } as unknown as AgentCompatibility;
		expect(visibleSuites(suites, "", compat, true).map((s) => s.id)).toEqual(["hd-harbor-bfcl"]);
		expect(visibleSuites(suites, "", compat, false).map((s) => s.id)).toEqual(["hd-harbor-bfcl", "longmemeval-s"]);
		expect(visibleSuites(suites, "memory", undefined, true).map((s) => s.id)).toEqual(["longmemeval-s"]);
		expect(visibleSuites(suites, "zzz", undefined, true, ["hd-harbor-bfcl"]).map((s) => s.id)).toEqual(["hd-harbor-bfcl"]);
	});
	it("names faults in the form's words", () => {
		expect(ftFaultName("x-y", [{ id: "x-y", label: "Catalog name" }])).toBe("Catalog name");
		expect(ftFaultName("some-new-fault")).toMatch(/^Some new fault$/);
	});
});
