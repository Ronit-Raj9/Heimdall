import { describe, expect, it } from "vitest";
import {
	agentDiffRedirect,
	calibrationRedirect,
	clustersRedirect,
	importsRedirect,
	inboxRedirect,
	suitesListRedirect,
	triageRedirect,
	trendsRedirect,
	whyAgentsFailRedirect,
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

	it("sends /clusters and /triage to the /failures tabs in one hop", () => {
		expect(clustersRedirect({ sig: "c_99", min: 2 })).toEqual({ to: "/failures", search: { sig: "c_99", min: 2, tab: "groups" } });
		expect(clustersRedirect({})).toEqual({ to: "/failures", search: { tab: "groups" } });
		for (const key of ["suite", "agent", "exp", "since", "min", "synthetic", "sig"]) {
			expect(clustersRedirect({ [key]: "v" }).search[key], key).toBe("v");
		}
		expect(triageRedirect({ exp: "e_1", pattern: "loop" })).toEqual({
			to: "/failures",
			search: { exp: "e_1", pattern: "loop", tab: "patterns" },
		});
		expect(triageRedirect({})).toEqual({ to: "/failures", search: { tab: "patterns" } });
	});

	it("replaces a tab the old URL carried with the new one", () => {
		expect(clustersRedirect({ tab: "x" }).search.tab).toBe("groups");
	});

	it("sends /inbox to /failures, mapping its tab and keeping every other key", () => {
		expect(inboxRedirect({})).toEqual({ to: "/failures", search: { tab: "production" } });
		const tabs: [string, string][] = [
			["inbox", "production"],
			["clusters", "groups"],
			["triage", "patterns"],
			["findings", "findings"],
			["connect", "production"],
			["nope", "production"],
		];
		for (const [from, to] of tabs) expect(inboxRedirect({ tab: from }).search.tab, from).toBe(to);
		// Every query key the old tabs read: inbox list, item panel, findings, groups, patterns.
		for (const key of ["state", "source", "item", "q", "finding", "suite", "agent", "exp", "since", "min", "synthetic", "sig", "pattern"]) {
			expect(inboxRedirect({ [key]: "v", tab: "inbox" }).search, key).toEqual({ [key]: "v", tab: "production" });
		}
		// A deep link into one item still opens it.
		expect(inboxRedirect({ item: "inc-1" })).toEqual({ to: "/failures", search: { item: "inc-1", tab: "production" } });
		expect(inboxRedirect({ tab: "findings", finding: "f_1" })).toEqual({ to: "/failures", search: { finding: "f_1", tab: "findings" } });
	});

	it("sends /why-agents-fail to the per-agent tab of /failures, its filters kept", () => {
		expect(whyAgentsFailRedirect({})).toEqual({ to: "/failures", search: { tab: "agents" } });
		expect(whyAgentsFailRedirect({ tab: "patterns" }).search.tab).toBe("agents"); // old patterns tab = per agent version (cc04f6abb)
		expect(whyAgentsFailRedirect({ tab: "agents" }).search.tab).toBe("agents");
		expect(whyAgentsFailRedirect({ tab: "groups", sig: "c_1" })).toEqual({ to: "/failures", search: { sig: "c_1", tab: "groups" } });
		expect(whyAgentsFailRedirect({ agent: "a1", version: 3, benchmark: "coding-core", practice: true })).toEqual({
			to: "/failures",
			search: { agent: "a1", version: 3, benchmark: "coding-core", practice: true, tab: "agents" },
		});
	});

	it("sends /imports straight to the Catalog tab of Benchmarks", () => {
		expect(importsRedirect()).toEqual({ to: "/suites", search: { tab: "catalog" } });
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