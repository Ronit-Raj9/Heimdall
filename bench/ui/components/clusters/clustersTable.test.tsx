import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { CLUSTER_LIST_FIXTURE } from "./clusters.fixtures";
import { ClustersSummary } from "./clustersSummary";
import { ClusterScopeBadge, ClustersTable } from "./clustersTable";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const clusters = CLUSTER_LIST_FIXTURE.clusters;
const [limiter, loop, toolError] = clusters;

describe("ClustersTable", () => {
	const html = renderToStaticMarkup(<ClustersTable clusters={clusters} selected={loop.signature} onSelect={() => undefined} />);

	it("keeps the server's order, largest count first", () => {
		const rows = [...html.matchAll(/data-testid="clusters-row-(c_[0-9a-f]+)"/g)].map((m) => m[1]);
		expect(rows).toEqual(clusters.map((c) => c.signature));
	});

	it("has every column", () => {
		for (const head of [
			">Group<",
			">Runs<",
			">Share<",
			">How widespread<",
			">Tasks<",
			">Agents<",
			">Conditions<",
			">Comparisons<",
			">Last seen<",
		]) {
			expect(html).toContain(head);
		}
	});

	it("shows count, share as a percent and the scope badge", () => {
		expect(html).toMatch(new RegExp(`data-testid="clusters-count-${limiter.signature}">6<`));
		expect(html).toMatch(new RegExp(`data-testid="clusters-share-${limiter.signature}">50%<`));
		expect(html).toMatch(new RegExp(`data-testid="clusters-share-${toolError.signature}">16.7%<`));
		expect(html).toMatch(new RegExp(`data-testid="clusters-scope-${limiter.signature}"[^>]*>Widespread<`));
		expect(html).toMatch(new RegExp(`data-testid="clusters-scope-${loop.signature}"[^>]*>One agent, one task<`));
	});

	it("shows two items per list and counts the rest", () => {
		const row = html.match(new RegExp(`data-testid="clusters-row-${limiter.signature}"[\\s\\S]*?</tr>`))?.[0] ?? "";
		expect(row).toContain("coding-core/t03");
		expect(row).toContain("coding-core/t04");
		expect(row).not.toContain(">coding-core/t01<");
		expect(row).toContain("+1 more");
	});

	it("marks a cluster that followed a config change and the open row", () => {
		expect(html).toContain(`data-testid="clusters-after-change-${limiter.signature}"`);
		expect(html).not.toContain(`data-testid="clusters-after-change-${loop.signature}"`);
		expect(html).toMatch(new RegExp(`data-testid="clusters-row-${loop.signature}"[^>]*data-state="selected"`));
		expect(html).toMatch(new RegExp(`aria-expanded="true"[^>]*data-testid="clusters-show-${loop.signature}"`));
		expect(html).toMatch(new RegExp(`aria-expanded="false"[^>]*data-testid="clusters-show-${limiter.signature}"`));
	});

	it("escapes titles: a quote in a title stays text", () => {
		expect(html).toContain("timeout after repeated bash &quot;pytest -q&quot;");
	});
});

describe("ClusterScopeBadge", () => {
	it("labels known scopes with a hint and shows an unknown one raw", () => {
		expect(renderToStaticMarkup(<ClusterScopeBadge scope="single_run" />)).toContain("One run");
		expect(renderToStaticMarkup(<ClusterScopeBadge scope="task" />)).toContain('title="One task, more than one agent setup."');
		expect(renderToStaticMarkup(<ClusterScopeBadge scope="galaxy" />)).toContain(">galaxy<");
	});
});

describe("ClustersSummary", () => {
	it("says N failures in M verified runs, the experiments read and the runs left out by reason", () => {
		const html = renderToStaticMarkup(<ClustersSummary data={CLUSTER_LIST_FIXTURE} />);
		expect(html).toMatch(/data-testid="clusters-summary-line">12 failures in 30 checked runs</);
		expect(html).toContain("from 2 comparisons");
		expect(html).toMatch(/data-testid="clusters-excluded-not-sealed".*?not sealed.*?2 runs/);
		expect(html).toMatch(/data-testid="clusters-excluded-synthetic".*?synthetic.*?1 run</);
		expect(html).toContain("1 group with fewer than 2 runs is not shown.");
	});

	it("uses singular forms and says when nothing was left out", () => {
		const html = renderToStaticMarkup(
			<ClustersSummary data={{ ...CLUSTER_LIST_FIXTURE, runs: 1, failures: 1, experiments: [], excluded: [], hidden: 0 }} />,
		);
		expect(html).toContain("1 failure in 1 checked run");
		expect(html).toContain("Every run is counted.");
		expect(html).not.toContain("clusters-summary-hidden");
		expect(html).not.toContain("clusters-summary-experiments");
	});

	it("links each task to its suite's task row and each agent to its config", () => {
		const html = renderToStaticMarkup(<ClustersTable clusters={clusters} selected={null} onSelect={() => undefined} />);
		const [suite, id] = (limiter.tasks ?? [])[0].split("/");
		expect(html).toContain(`href="/suites/${suite}?tab=tasks&amp;task=${id}"`);
		expect(html).toContain(`href="/agents?agent=${(limiter.agents ?? [])[0]}"`);
	});
});