import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

const calls = vi.hoisted(() => ({ light: 0, full: 0 }));

vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => {
		calls.light++;
		return okQuery({ suites: [] });
	},
}));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...(await orig<Record<string, unknown>>()),
	useGetSuitesQuery: () => {
		calls.full++;
		return okQuery({ suites: [] });
	},
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (orig) => ({
	...(await orig<Record<string, unknown>>()),
	useGetEvaluationPacksQuery: () => okQuery([]),
	useGetAgentCompatibilityQuery: () => okQuery(undefined),
}));
vi.mock("@/lib/store/apis/referenceApi", async (orig) => ({
	...(await orig<Record<string, unknown>>()),
	useGetReferenceBoardQuery: () => okQuery({ entries: [] }),
}));
vi.mock("./ownTasks", () => ({ OwnTasksCard: () => null }));
vi.mock("@/components/redteam/redTeamPanel", () => ({ RedTeamPanel: () => null }));

import { Step3BenchmarksWith } from "./step3Benchmarks";

// N194: the benchmark step of the evaluate wizard reads the light list (GET /suites?fields=light), never the full one.
describe("step 3 benchmarks list", () => {
	it("loads the light list", () => {
		render(<Step3BenchmarksWith draft={{ agent: "", benchmarks: [] }} onChange={() => undefined} />);
		expect(calls.light).toBeGreaterThan(0);
		expect(calls.full).toBe(0);
	});
});