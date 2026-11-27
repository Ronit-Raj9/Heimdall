import "@/lib/i18n";

import type { AgentDiffParams, AgentDiffResponse, AgentDiffVersionsResponse } from "@/lib/types/bench";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AgentDiffTab from "./agentDiffTab";

const query = vi.hoisted(() => ({
	diff: {} as Record<string, unknown>,
	versions: {} as Record<string, unknown>,
	diffArgs: [] as unknown[],
}));

vi.mock("@/lib/store/apis/agentDiffApi", () => ({
	useGetAgentDiffQuery: (args: unknown) => {
		query.diffArgs.push(args);
		return query.diff;
	},
	useGetAgentDiffVersionsQuery: () => query.versions,
}));

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search?: Record<string, string>; children: React.ReactNode }) => {
		const qs = new URLSearchParams(search ?? {}).toString();
		return (
			<a href={qs ? `${to}?${qs}` : to} {...rest}>
				{children}
			</a>
		);
	},
}));

const fixtureDir = path.resolve(__dirname, "../../../public/fixtures");
const readFixture = <T,>(file: string): T => JSON.parse(fs.readFileSync(path.join(fixtureDir, file), "utf8")) as T;
const report = readFixture<AgentDiffResponse>("agents.diff.json");
const versionList = readFixture<AgentDiffVersionsResponse>("agents.diff.versions.json");
const HASH_NEW = "sha256:67cd7a069ba6e6141a645638240d97c5062022fa81126e36300d8bc0006730cd";
const HASH_OLD = "sha256:895fa8468a3bcbdb1351f9dcdc550dc492c5df1daf7bbd092e4543f5c1c83758";

const idle = {
	isLoading: false,
	isFetching: false,
	isError: false,
	isSuccess: false,
	data: undefined,
	error: undefined,
	refetch: () => undefined,
};
const loaded = (data: unknown) => ({ ...idle, isSuccess: true, data });

function render(search: string): string {
	return renderToStaticMarkup(
		<NuqsTestingAdapter searchParams={search}>
			<AgentDiffTab />
		</NuqsTestingAdapter>,
	);
}

/** Text with tags removed, to assert reading order. */
const text = (html: string) => html.replace(/<[^>]+>/g, " ");

describe("AgentDiffTab", () => {
	beforeEach(() => {
		query.diff = { ...idle };
		query.versions = loaded(versionList);
		query.diffArgs = [];
	});

	it("asks for two versions before there is a selection, and asks the server nothing", () => {
		const html = render("");
		expect(html).toContain('data-testid="agentdiff-picker"');
		expect(html).toContain('data-testid="agentdiff-pick"');
		expect(query.diffArgs.every((args) => typeof args === "symbol")).toBe(true);
	});

	it("says when no version has sealed runs", () => {
		query.versions = loaded({ mode: "live", versions: [] });
		expect(render("")).toContain('data-testid="agentdiff-no-versions"');
	});

	it("compares B with the previous version of its agent when only B is set", () => {
		query.diff = loaded(report);
		const html = render(`?b=${encodeURIComponent(HASH_NEW)}`);
		expect(query.diffArgs).toContainEqual({ a: HASH_OLD, b: HASH_NEW } satisfies AgentDiffParams);
		expect(html).toContain('data-testid="agentdiff-suggested-note"');
		expect(html).toContain("A is the previous version");
	});

	it("passes the URL params to the server", () => {
		query.diff = loaded(report);
		render(`?a=abcdef12&b=e_x:B&suite=coding-core%401.0.0&condition=probe-x&synthetic=1`);
		expect(query.diffArgs).toContainEqual({ a: "abcdef12", b: "e_x:B", suite: "coding-core@1.0.0", condition: "probe-x", synthetic: "1" });
	});

	it("draws the report: summary, versions, config groups, flipped tasks, metrics and gates", () => {
		query.diff = loaded(report);
		const html = render(`?a=${encodeURIComponent(HASH_OLD)}&b=e_synthetic-codewins:B`);
		expect(html).toContain('data-testid="agentdiff-synthetic-banner"');
		expect(html).toMatch(/data-testid="agentdiff-summary-text"[^>]*>B \(B: code-wins note\) passes one more task than A/);
		expect(html).toMatch(/data-testid="agentdiff-conclusion"[^>]*>Cannot tell/);
		expect(html).toContain("too few to tell these setups apart");
		expect(html).toMatch(/data-testid="agentdiff-net"[^>]*>Net \+1 tasks/);
		// Config groups in report order, the redacted marker, a collapsible diff and a hash-only note.
		const order = ["agentdiff-group-prompt_notes", "agentdiff-group-tools", "agentdiff-group-budgets", "agentdiff-group-gateway"];
		const at = order.map((id) => html.indexOf(`data-testid="${id}"`));
		expect(at.every((i) => i >= 0)).toBe(true);
		expect([...at].sort((x, y) => x - y)).toEqual(at);
		expect(html).toContain('data-testid="agentdiff-redacted-gateway-headers-x-api-key"');
		expect(html).toContain('data-testid="agentdiff-file-CLAUDE-md-details"');
		expect(html).toContain("When the code and the docs disagree, the code wins.");
		expect(html).toContain("content not at hand; compared by hash");
		// Flips and moves only by default, with run comparison links.
		expect(html).toContain('data-testid="agentdiff-task-t02"');
		expect(html).toContain('data-testid="agentdiff-task-t03"');
		expect(html).not.toContain('data-testid="agentdiff-task-t01"');
		expect(html).toContain('href="/runs?pair=p_ltosmymyqy"');
		expect(html).toContain('href="/runs?pair=p_synthprov03"');
		expect(html).toContain('href="/runs?pair=p_synthcode03"');
		expect(html).toContain("Show all 3 tasks");
		expect(html).toContain("Only B ran 1 task, left out: t04.");
		// Metrics read with direction; unknown stays unknown, never 0.
		expect(html).toMatch(/data-testid="agentdiff-metric-cost_usd" data-direction="better"/);
		expect(html).toMatch(/data-testid="agentdiff-metric-steps" data-direction="worse"/);
		expect(text(html)).toContain("-0.009");
		expect(html).toMatch(/data-testid="agentdiff-metric-tokens"[\s\S]*?unknown/);
		// The new gate comes first.
		expect(html.indexOf("agentdiff-gate-policy-no_network")).toBeLessThan(html.indexOf("agentdiff-gate-budget-turns"));
		expect(html).toContain("new in B");
		// Runs left out per version.
		expect(html).toContain("seal does not verify: 1 run");
		expect(html).toMatch(/data-testid="agentdiff-version-b-excluded"[^>]*><span[^>]*>No run left out/);
	});

	it("shows the server's message when the diff fails", () => {
		query.diff = {
			...idle,
			isError: true,
			error: { status: 400, data: { mode: "live", error: { type: "bad_query", message: "no common task" } } },
		};
		const html = render(`?a=${encodeURIComponent(HASH_OLD)}&b=${encodeURIComponent(HASH_NEW)}`);
		expect(html).toContain('data-testid="agentdiff-error"');
		expect(html).toContain("no common task");
	});

	it("shows a skeleton while the diff loads", () => {
		query.diff = { ...idle, isLoading: true };
		expect(render(`?agent=codex`)).toContain('data-testid="agentdiff-loading"');
		expect(query.diffArgs).toContainEqual({ agent: "codex" });
	});

	it("says the config is identical when nothing changed", () => {
		query.diff = loaded({ ...report, config: { identical: true, changes: 0, groups: [] } });
		expect(render(`?a=${encodeURIComponent(HASH_OLD)}&b=${encodeURIComponent(HASH_NEW)}`)).toContain(
			'data-testid="agentdiff-config-identical"',
		);
	});
});