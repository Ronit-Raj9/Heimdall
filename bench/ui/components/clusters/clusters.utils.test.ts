import { describe, expect, it } from "vitest";
import {
	CLUSTER_LAYER_KINDS,
	CLUSTER_SCOPES,
	clusterParams,
	excludesSynthetic,
	formatExpList,
	formatShare,
	hasClusterFilters,
	isClusterLayerKind,
	isClusterScope,
	isHiddenTestLayer,
	isNotFound,
	parseExpList,
	shownItems,
	type ClusterQuery,
} from "./clusters.utils";

const empty: ClusterQuery = { suite: null, agent: null, exp: null, since: null, min: null, synthetic: null };

describe("parseExpList and formatExpList", () => {
	it("trims, drops empty ids and keeps the first of a duplicate", () => {
		expect(parseExpList(" e_1, ,e_2,e_1 ,")).toEqual(["e_1", "e_2"]);
		expect(parseExpList("")).toEqual([]);
		expect(parseExpList(null)).toEqual([]);
	});

	it("joins with commas, or drops the param when empty", () => {
		expect(formatExpList(["e_1", "e_2"])).toBe("e_1,e_2");
		expect(formatExpList([])).toBeNull();
	});
});

describe("clusterParams", () => {
	it("sends nothing for an empty query, so every server default applies", () => {
		expect(clusterParams(empty)).toEqual({});
		expect(hasClusterFilters(empty)).toBe(false);
	});

	it("sends set filters, normalizing the experiment list", () => {
		const params = clusterParams({
			suite: " coding-core ",
			agent: "codex-default",
			exp: "e_1, e_2,,e_1",
			since: "2026-11-01",
			min: 2,
			synthetic: "include",
		});
		expect(params).toEqual({
			suite: "coding-core",
			agent: "codex-default",
			exp: "e_1,e_2",
			since: "2026-11-01",
			min: 2,
			synthetic: "include",
		});
	});

	it("leaves out blank text and a minimum below 1", () => {
		expect(clusterParams({ ...empty, suite: "  ", exp: " , ", min: 0 })).toEqual({});
		expect(clusterParams({ ...empty, min: -3 })).toEqual({});
		expect(hasClusterFilters({ ...empty, synthetic: "only" })).toBe(true);
	});
});

describe("formatShare", () => {
	it("shows a percent with at most one decimal", () => {
		expect(formatShare(0.5, "en")).toBe("50%");
		expect(formatShare(0.1667, "en")).toBe("16.7%");
	});

	it("clamps values outside 0..1 and reads a non-number as 0", () => {
		expect(formatShare(1.4, "en")).toBe("100%");
		expect(formatShare(-0.2, "en")).toBe("0%");
		expect(formatShare(Number.NaN, "en")).toBe("0%");
	});
});

describe("shownItems", () => {
	it("shows the first two and counts the rest", () => {
		expect(shownItems(["a", "b", "c", "d"])).toEqual({ shown: ["a", "b"], more: 2 });
		expect(shownItems(["a"])).toEqual({ shown: ["a"], more: 0 });
		expect(shownItems(null)).toEqual({ shown: [], more: 0 });
	});
});

describe("scope and layer kinds", () => {
	it("knows the contract's scopes and layer kinds and nothing else", () => {
		expect(CLUSTER_SCOPES).toEqual(["single_run", "agent_and_task", "agent", "task", "systemic"]);
		expect(CLUSTER_LAYER_KINDS).toHaveLength(11);
		expect(isClusterScope("systemic")).toBe(true);
		expect(isClusterScope("galaxy")).toBe(false);
		expect(isClusterLayerKind("failed_tests")).toBe(true);
		expect(isClusterLayerKind("vibes")).toBe(false);
	});

	it("treats only a failed_tests layer with ids as hidden test names", () => {
		expect(isHiddenTestLayer({ kind: "failed_tests", label: "test_a", items: ["test_a"] })).toBe(true);
		expect(isHiddenTestLayer({ kind: "failed_tests", label: "2 hidden tests" })).toBe(false);
		expect(isHiddenTestLayer({ kind: "state_checks", label: "ticket_closed", items: ["ticket_closed"] })).toBe(false);
	});
});

describe("excludesSynthetic and isNotFound", () => {
	it("sees synthetic runs left out only when there are some", () => {
		expect(excludesSynthetic([{ reason: "synthetic", runs: 3 }])).toBe(true);
		expect(excludesSynthetic([{ reason: "synthetic", runs: 0 }])).toBe(false);
		expect(excludesSynthetic([{ reason: "not sealed", runs: 3 }])).toBe(false);
		expect(excludesSynthetic(null)).toBe(false);
	});

	it("tells a 404 from other failures", () => {
		expect(isNotFound({ status: 404, data: {} })).toBe(true);
		expect(isNotFound({ status: 500 })).toBe(false);
		expect(isNotFound(undefined)).toBe(false);
	});
});