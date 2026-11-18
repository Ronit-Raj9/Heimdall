import "@/lib/i18n";

import { CLUSTER_DETAIL_FIXTURE, CLUSTER_LIST_FIXTURE } from "@/components/clusters/clusters.fixtures";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ClusterRun, Grid } from "@/lib/types/bench";
import { skipToken } from "@reduxjs/toolkit/query/react";
import fs from "node:fs";
import path from "node:path";
import ClustersTab, { EmptyClusters, RunPairLink } from "./clustersTab";

const state = vi.hoisted(() => ({
	url: {} as Record<string, string | number | null>,
	list: {} as Record<string, unknown>,
	detail: {} as Record<string, unknown>,
	listArgs: [] as unknown[],
	detailArgs: [] as unknown[],
	gridArgs: [] as unknown[],
	grid: undefined as unknown,
}));

// The URL query is a plain object; parsers are not exercised here.
vi.mock("nuqs", () => ({
	parseAsString: {},
	parseAsInteger: {},
	parseAsStringLiteral: () => ({}),
	useQueryState: (key: string) => [state.url[key] ?? null, () => Promise.resolve(new URLSearchParams())],
}));

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search: { pair: string }; children: React.ReactNode }) => (
		<a href={`${to}?pair=${search.pair}`} {...rest}>
			{children}
		</a>
	),
}));

// Real error formatting; only the requests are faked.
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetClustersQuery: (args: unknown) => {
		state.listArgs.push(args);
		return state.list;
	},
	useGetClusterQuery: (args: unknown) => {
		state.detailArgs.push(args);
		return state.detail;
	},
	useGetGridQuery: (args: unknown) => {
		state.gridArgs.push(args);
		return { isLoading: false, data: args === skipToken ? undefined : state.grid };
	},
}));

const idle = { isLoading: false, isFetching: false, isError: false, data: undefined, error: undefined, refetch: () => undefined };

describe("ClustersTab", () => {
	beforeEach(() => {
		state.url = {};
		state.list = { ...idle };
		state.detail = { ...idle };
		state.listArgs = [];
		state.detailArgs = [];
		state.gridArgs = [];
		state.grid = undefined;
	});

	it("shows a skeleton while loading", () => {
		state.list = { ...idle, isLoading: true };
		const html = renderToStaticMarkup(<ClustersTab />);
		expect(html).toContain('data-testid="clusters-loading"');
		expect(html).toContain('data-testid="clusters-banner"');
	});

	it("shows the server's message on failure, with a reset when filters are set", () => {
		state.url = { suite: "nope" };
		state.list = { ...idle, isError: true, error: { status: 400, data: { error: { message: "unknown suite nope" } } } };
		const html = renderToStaticMarkup(<ClustersTab />);
		expect(html).toContain('data-testid="clusters-error"');
		expect(html).toContain("unknown suite nope");
		expect(html).toContain('data-testid="clusters-reset-filters"');
	});

	it("sends URL filters to the server and lists clusters with the scope line", () => {
		state.url = { agent: "codex-default", exp: "e_1, e_2", min: 2, synthetic: "include" };
		state.list = { ...idle, data: CLUSTER_LIST_FIXTURE };
		const html = renderToStaticMarkup(<ClustersTab />);
		expect(state.listArgs[0]).toEqual({ agent: "codex-default", exp: "e_1,e_2", min: 2, synthetic: "include" });
		expect(html).toContain('data-testid="clusters-table"');
		expect(html).toContain("12 failures in 30 verified runs");
		expect(html).toContain('data-testid="mode-badge-synthetic"');
		expect(html).not.toContain('data-testid="cluster-panel"');
	});

	it("opens the cluster named by sig below the table, with the same filters", () => {
		state.url = { sig: "c_99aabbccddeeff00", suite: "support-desk" };
		state.list = { ...idle, data: CLUSTER_LIST_FIXTURE };
		state.detail = { ...idle, data: CLUSTER_DETAIL_FIXTURE };
		const html = renderToStaticMarkup(<ClustersTab />);
		expect(state.detailArgs[0]).toEqual({ signature: "c_99aabbccddeeff00", suite: "support-desk" });
		expect(html).toContain('data-testid="cluster-detail-c_99aabbccddeeff00"');
		expect(html).toMatch(/data-testid="clusters-row-c_99aabbccddeeff00"[^>]*data-state="selected"/);
	});

	it("says a signature outside the filtered set is not found, not an error", () => {
		state.url = { sig: "c_0000000000000000" };
		state.list = { ...idle, data: CLUSTER_LIST_FIXTURE };
		state.detail = { ...idle, isError: true, error: { status: 404, data: { error: { message: "no such cluster" } } } };
		const html = renderToStaticMarkup(<ClustersTab />);
		expect(html).toContain('data-testid="cluster-detail-not-found"');
		expect(html).not.toContain('data-testid="cluster-detail-error"');
	});

	it("shows an empty state when there are no clusters", () => {
		state.list = { ...idle, data: { ...CLUSTER_LIST_FIXTURE, clusters: [], hidden: 0 } };
		const html = renderToStaticMarkup(<ClustersTab />);
		expect(html).toContain('data-testid="clusters-empty"');
		expect(html).not.toContain('data-testid="clusters-table"');
	});
});

describe("EmptyClusters", () => {
	const base = { ...CLUSTER_LIST_FIXTURE, clusters: [], hidden: 0 };
	const render = (data: typeof base) => renderToStaticMarkup(<EmptyClusters data={data} onShowSynthetic={() => undefined} />);

	it("says why: no runs, no failures, or every cluster below the minimum", () => {
		expect(render({ ...base, runs: 0, failures: 0, excluded: [] })).toContain("No verified runs match these filters.");
		expect(render({ ...base, runs: 5, failures: 0, excluded: [] })).toContain("all 5 verified runs in scope passed.");
		expect(render({ ...base, hidden: 3, excluded: [] })).toContain("Every cluster is below the minimum count.");
	});

	it("offers synthetic runs only when they were left out", () => {
		expect(render(base)).toContain('data-testid="clusters-show-synthetic"');
		expect(render({ ...base, excluded: [{ reason: "not sealed", runs: 2 }] })).not.toContain("clusters-show-synthetic");
		expect(render({ ...base, filters: { ...base.filters, synthetic: "include" } })).not.toContain("clusters-show-synthetic");
	});
});
describe("RunPairLink", () => {
	const grid = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../public/fixtures/experiments.grid.json"), "utf8")) as Grid;
	const run = CLUSTER_DETAIL_FIXTURE.runs[1];
	const render = (r: ClusterRun) => renderToStaticMarkup(<RunPairLink run={r} />);

	beforeEach(() => {
		state.gridArgs = [];
		state.grid = grid;
	});

	it("links straight to the server's pair_id without reading the grid", () => {
		const html = render(CLUSTER_DETAIL_FIXTURE.runs[0]);
		expect(html).toMatch(/href="\/runs\?pair=p_ltosmymyqy"[^>]*data-testid="cluster-run-link-r_fmw1z7ouim"/);
		expect(state.gridArgs).toEqual([skipToken]);
	});

	it("falls back to the experiment's grid for a run without pair_id", () => {
		// r_qolshdu5jg is pair p_gszjh69z4k in the grid fixture.
		const html = render({ ...run, run_id: "r_qolshdu5jg", exp_id: "e_synthetic-codewins" });
		expect(html).toMatch(/href="\/runs\?pair=p_gszjh69z4k"[^>]*data-testid="cluster-run-link-r_qolshdu5jg"/);
		expect(state.gridArgs).toEqual(["e_synthetic-codewins"]);
	});

	it("shows plain text when neither the server nor the grid knows the pair", () => {
		const html = render(run);
		expect(html).toContain('data-testid="cluster-run-id-r_tool000001"');
		expect(html).toContain("Pair not in the grid");
		expect(html).not.toContain("href=");
	});
});