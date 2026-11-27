import "@/lib/i18n";

import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ClusterDetailPanel, ClusterLayers, ClusterRunLink, ErrorText } from "./clusterDetail";
import { CLUSTER_DETAIL_FIXTURE, CLUSTER_LIST_FIXTURE } from "./clusters.fixtures";

// A plain anchor stands in for the router's Link, so the target URL is visible in the markup.
vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search: { pair: string }; children: React.ReactNode }) => (
		<a href={`${to}?pair=${search.pair}`} {...rest}>
			{children}
		</a>
	),
}));

const limiter = CLUSTER_LIST_FIXTURE.clusters[0];
const render = (detail = CLUSTER_DETAIL_FIXTURE) =>
	renderToStaticMarkup(
		<ClusterDetailPanel
			detail={detail}
			onClose={() => undefined}
			onOpen={() => undefined}
			renderRunLink={(run) => <ClusterRunLink runId={run.run_id} pairId={run.pair_id ?? null} />}
		/>,
	);

describe("ClusterDetailPanel", () => {
	const html = render();

	it("names the cluster with its outcome, scope, size and signature", () => {
		expect(html).toContain('data-testid="cluster-detail-c_99aabbccddeeff00"');
		expect(html).toContain("tool error: desk.refund amount must be positive");
		expect(html).toContain("2 runs, 16.7% of failures");
		expect(html).toMatch(/data-testid="cluster-detail-scope"[^>]*>One agent, one task</);
		expect(html).toContain(">c_99aabbccddeeff00<");
	});

	it("lists the layers most specific first, with where the error line came from", () => {
		const kinds = [...html.matchAll(/data-testid="cluster-layer-([a-z_]+)"/g)].map((m) => m[1]);
		expect(kinds).toEqual(["outcome", "error", "last_tool"]);
		expect(html).toContain("from tool_proxy");
		expect(html).toContain(">Last tool<");
	});

	it("shows the normalized error text as inert text in a pre block", () => {
		const block = html.match(/<pre[^>]*data-testid="cluster-error-text"[^>]*><code[^>]*>(.*?)<\/code><\/pre>/)?.[1];
		expect(block).toBe(
			"desk.refund &lt;img src=x onerror=&quot;alert(1)&quot;&gt; amount must be positive https://example.invalid/x **bold**",
		);
		// Never markup, never a link, never markdown.
		expect(html).not.toContain("<img");
		expect(html).not.toContain('href="https://example.invalid');
		expect(html).not.toContain("<strong>");
		expect(html).not.toContain("<b>");
	});

	it("links each run id to its matched pair, and says when the pair is unknown", () => {
		expect(html).toMatch(/href="\/runs\?pair=p_ltosmymyqy"[^>]*data-testid="cluster-run-link-r_fmw1z7ouim"[^>]*>r_fmw1z7ouim</);
		expect(html).toContain('data-testid="cluster-run-id-r_tool000001"');
		expect(html).toContain("Pair not in the grid");
		expect(html).toMatch(/data-testid="cluster-run-r_tool000001".*?support-desk\/refund-01.*?tool-fault · Setup B/);
	});

	it("shows each run's last tool and error line as text", () => {
		expect(html).toMatch(/data-testid="cluster-run-r_fmw1z7ouim".*?desk\.refund.*?errored/);
		expect(html).toMatch(/data-testid="cluster-run-error-r_fmw1z7ouim"[^>]*>desk\.refund &lt;img/);
	});

	it("has no config change notice or related list when the cluster has neither", () => {
		expect(html).not.toContain('data-testid="cluster-after-change"');
		expect(html).not.toContain('data-testid="cluster-related"');
	});

	it("shows the config change notice and related clusters that open in the panel", () => {
		const out = render({ ...CLUSTER_DETAIL_FIXTURE, cluster: limiter });
		expect(out).toContain('data-testid="cluster-after-change"');
		expect(out).toContain("claude-code-sonnet changed settings from aaaaaaaa…1111 to cccccccc…3333");
		expect(out).toMatch(/<button[^>]*data-testid="cluster-related-c_1122334455667788"/);
		expect(out).toContain("3 runs");
		expect(out).toContain("same task t03");
		expect(out).not.toContain('data-testid="cluster-error-text"');
	});

	it("says when the cluster has no runs", () => {
		expect(render({ ...CLUSTER_DETAIL_FIXTURE, runs: [] })).toContain('data-testid="cluster-runs-empty"');
	});
});

describe("hidden test names", () => {
	it("are listed the way the run page lists grade.json's failed tests", () => {
		const html = renderToStaticMarkup(<ClusterLayers layers={limiter.layers} />);
		const list = html.match(/data-testid="cluster-hidden-tests".*?<\/ul>/)?.[0] ?? "";
		expect(list).toContain(">Failed hidden checks<");
		expect(list).toContain('<li class="text-chart-error-ink font-mono text-xs break-all">test_limiter_window</li>');
	});

	it("use the same list markup as the run page's TestList", () => {
		// The run page's list (components/runs/claimVsResult.tsx) and this one must look the same.
		const runPage = fs.readFileSync(path.resolve(__dirname, "../runs/claimVsResult.tsx"), "utf8");
		const ours = fs.readFileSync(path.resolve(__dirname, "clusterDetail.tsx"), "utf8");
		const itemClass = 'className="text-chart-error-ink font-mono text-xs break-all"';
		expect(runPage).toContain(itemClass);
		expect(ours).toContain(itemClass);
	});

	it("are not a hidden test list when the layer has no ids", () => {
		const html = renderToStaticMarkup(<ClusterLayers layers={[{ kind: "failed_tests", label: "4 hidden tests" }]} />);
		expect(html).not.toContain("cluster-hidden-tests");
		expect(html).toContain(">4 hidden tests<");
	});

	it("never come from other item layers", () => {
		const html = renderToStaticMarkup(<ClusterLayers layers={[{ kind: "gates", label: "g1, g2", items: ["g1_protected_paths", "g2"] }]} />);
		expect(html).not.toContain("cluster-hidden-tests");
		expect(html).toContain(">g1_protected_paths<");
		expect(html).toContain(">Safety checks triggered<");
	});

	it("label an unknown layer kind with its raw value", () => {
		expect(renderToStaticMarkup(<ClusterLayers layers={[{ kind: "novel" as "loop", label: "x" }]} />)).toContain(">novel<");
	});
});

describe("ErrorText and ClusterRunLink", () => {
	it("keeps whitespace and escapes markup", () => {
		const html = renderToStaticMarkup(<ErrorText text={"line 1\n  <b>line 2</b>"} testId="t" />);
		expect(html).toContain("line 1\n  &lt;b&gt;line 2&lt;/b&gt;");
	});

	it("shows a plain id while the grid loads", () => {
		const html = renderToStaticMarkup(<ClusterRunLink runId="r_x" pairId={undefined} />);
		expect(html).toContain('data-testid="cluster-run-id-r_x"');
		expect(html).not.toContain("Pair not in the grid");
		expect(html).not.toContain("href=");
	});
});