import { describe, expect, it } from "vitest";
import {
	agentDiffRedirect,
	calibrationRedirect,
	clustersRedirect,
	importsRedirect,
	suitesListRedirect,
	triageRedirect,
	trendsRedirect,
} from "./u3Redirects";

// Every old U3 URL of the UI regroup and where it lands. Search objects are as TanStack Router
// parses them: numbers and booleans already decoded.
describe("U3 redirects", () => {
	it("sends /agents/diff to the Diff tab with its query", () => {
		expect(agentDiffRedirect({ a: "sha256:aaa", b: "sha256:bbb", synthetic: 1 })).toEqual({
			to: "/agents",
			search: { a: "sha256:aaa", b: "sha256:bbb", synthetic: 1, tab: "diff" },
		});
		expect(agentDiffRedirect({})).toEqual({ to: "/agents", search: { tab: "diff" } });
	});

	it("sends /trends to the Trends tab of the leaderboard, filters kept", () => {
		expect(trendsRedirect({ suite: "coding-core@1.0.0", metric: "cost_usd" })).toEqual({
			to: "/leaderboard",
			search: { suite: "coding-core@1.0.0", metric: "cost_usd", tab: "trends" },
		});
		expect(trendsRedirect({})).toEqual({ to: "/leaderboard", search: { tab: "trends" } });
	});

	it("sends /clusters and /triage to the inbox tabs", () => {
		expect(clustersRedirect({ sig: "c_99", min: 2 })).toEqual({ to: "/inbox", search: { sig: "c_99", min: 2, tab: "clusters" } });
		expect(triageRedirect({ exp: "e_1", pattern: "loop" })).toEqual({
			to: "/inbox",
			search: { exp: "e_1", pattern: "loop", tab: "triage" },
		});
		expect(triageRedirect({})).toEqual({ to: "/inbox", search: { tab: "triage" } });
	});

	it("replaces a tab the old URL carried with the new one", () => {
		expect(clustersRedirect({ tab: "x" }).search.tab).toBe("clusters");
	});

	it("sends /imports to the Add a suite flow", () => {
		expect(importsRedirect()).toEqual({ to: "/suites/add", search: {} });
	});

	it("sends /calibration?suite=X to that suite's Calibration tab, and to the list without one", () => {
		expect(calibrationRedirect({ suite: "support-desk" })).toEqual({
			to: "/suites/$suiteId",
			params: { suiteId: "support-desk" },
			search: { tab: "calibration" },
		});
		expect(calibrationRedirect({})).toEqual({ to: "/suites", search: {} });
		expect(calibrationRedirect({ suite: "  " })).toEqual({ to: "/suites", search: {} });
	});

	it("sends the old /suites?suite=X screen to /suites/X with the task table's query", () => {
		expect(suitesListRedirect({ suite: "coding-core", task: "t01", kind: "bugfix" })).toEqual({
			to: "/suites/$suiteId",
			params: { suiteId: "coding-core" },
			search: { task: "t01", kind: "bugfix" },
		});
		expect(suitesListRedirect({})).toBeNull();
		expect(suitesListRedirect({ kind: "bugfix" })).toBeNull();
	});
});