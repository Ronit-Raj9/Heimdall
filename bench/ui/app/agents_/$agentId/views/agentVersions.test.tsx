import "@/lib/i18n";

import type { AgentVersion, AgentVersionDetail, ReportCardResponse } from "@/lib/store/apis/evaluationsApi";
import { agentReportCardRequest } from "@/lib/store/apis/evaluationsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { lineDiff } from "./lineDiff";
import ReportCardTab, { isEmptyReport, modelOptions } from "./reportCardTab";
import VersionsTab, { sortVersions } from "./versionsTab";

const state = vi.hoisted(() => ({
	versions: [] as AgentVersion[],
	detail: {} as Record<number, AgentVersionDetail>,
	report: undefined as (ReportCardResponse & { models?: string[] }) | undefined,
	reportError: undefined as unknown,
	reportArgs: [] as unknown[],
	search: {} as Record<string, unknown>,
	navigated: [] as unknown[],
}));

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useGetAgentVersionsQuery: () => ({ data: { agent: "codex", versions: state.versions }, isLoading: false, isError: false }),
	useGetAgentVersionQuery: (arg: { version: number } | symbol) =>
		typeof arg === "object" ? { data: state.detail[arg.version], isLoading: false, isError: false } : { data: undefined },
	useCreateAgentVersionMutation: () => [vi.fn(), { isLoading: false, isError: false }],
	useGetAgentReportCardQuery: (arg: unknown) => {
		state.reportArgs.push(arg);
		return { data: state.report, error: state.reportError, isLoading: false, isError: Boolean(state.reportError) };
	},
}));

// The Report card tab reads the agent card through agentCardApi (fix wave 3, N206).
vi.mock("@/lib/store/apis/agentCardApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/agentCardApi")>()),
	useGetAgentCardQuery: (arg: unknown) => {
		state.reportArgs.push(arg);
		return { data: state.report, error: state.reportError, isLoading: false, isError: Boolean(state.reportError) };
	},
}));

vi.mock("@/lib/store/apis/failurePatternsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/failurePatternsApi")>()),
	useGetEvaluationFailurePatternsQuery: () => ({
		data: {
			evaluation: "ev_1",
			groups: [
				{
					key: "A",
					label: "A",
					agent: "codex",
					version: 2,
					models: ["gpt-x"],
					runs: 10,
					failed: 4,
					experiments: ["e1"],
					last_at: "",
					patterns: [
						{
							pattern: "pattern_loop",
							label: "Loop",
							description: "same call repeated",
							count: 3,
							observed: 10,
							unknown: 0,
							examples: [{ run_id: "r1", exp_id: "e1", benchmark: "hd-harbor-tb2", task: "t1", outcome: "fail" }],
						},
						{ pattern: "pattern_ignored_error", label: "Ignored error", description: "", count: 0, observed: 10, unknown: 0, examples: [] },
					],
				},
			],
		},
	}),
}));

vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetClustersQuery: () => ({ data: { clusters: [] } }),
}));

vi.mock("@/lib/store/apis/modelsApi", () => ({
	useGetModelsQuery: () => ({ data: { providers: [{ models: [{ id: "gpt-x" }, { id: "claude-y" }] }, { models: [{ id: "gpt-x" }] }] } }),
}));

vi.mock("@tanstack/react-router", () => ({
	useSearch: () => state.search,
	useNavigate: () => (opts: unknown) => state.navigated.push(opts),
	Link: ({
		to,
		search,
		params: _params,
		children,
		...rest
	}: {
		to: string;
		search?: Record<string, string>;
		params?: unknown;
		children: React.ReactNode;
	}) => (
		<a href={search ? `${to}?${new URLSearchParams(search).toString()}` : to} {...rest}>
			{children}
		</a>
	),
}));

const v = (version: number, note = ""): AgentVersion => ({
	version,
	hash: `h${version}`,
	created_at: "2026-11-20T10:00:00Z",
	created_by: "raj",
	note,
	runs: version * 3,
});

describe("agent versions tab", () => {
	it("lists versions newest first with note, author and runs", () => {
		state.versions = [v(1, "first"), v(2, "tighter prompt")];
		const html = renderToStaticMarkup(<VersionsTab agentId="codex" />);
		expect(html).toContain('data-testid="agent-versions-table"');
		expect(html.indexOf("agent-version-v2")).toBeLessThan(html.indexOf("agent-version-v1"));
		expect(html).toContain("tighter prompt");
		expect(html).toContain("raj");
		expect(html).toContain('data-testid="agent-version-view-v1"');
		expect(html).toContain("Save as new version");
	});

	it("shows an empty note with no versions", () => {
		state.versions = [];
		const html = renderToStaticMarkup(<VersionsTab agentId="codex" />);
		expect(html).toContain('data-testid="agent-versions-empty"');
	});

	it("sorts versions without mutating input", () => {
		const input = [v(1), v(3), v(2)];
		expect(sortVersions(input).map((x) => x.version)).toEqual([3, 2, 1]);
		expect(input.map((x) => x.version)).toEqual([1, 3, 2]);
	});
});

describe("lineDiff", () => {
	it("is empty for identical text", () => {
		expect(lineDiff("a\nb\n", "a\nb\n", { a: "v1", b: "v2" })).toBe("");
	});
	it("marks removed and added lines", () => {
		const patch = lineDiff("id: x\nmodel: a\nsteps: 3\n", "id: x\nmodel: b\nsteps: 3\n", { a: "v1", b: "v2" });
		expect(patch.split("\n")).toEqual(["--- v1", "+++ v2", " id: x", "-model: a", "+model: b", " steps: 3", ""]);
	});
});

describe("report card tab", () => {
	it("shows the evaluate call to action when there is no report card", () => {
		state.report = undefined;
		state.reportError = { status: 404, data: {} };
		const html = renderToStaticMarkup(<ReportCardTab agentId="codex" />);
		expect(html).toContain('data-testid="agent-report-card-empty"');
		expect(html).toContain('href="/evaluate?agent=codex"');
		expect(html).toContain("Evaluate this agent");
	});

	it("renders the report card with version and model pickers", () => {
		state.versions = [v(1), v(2)];
		state.reportError = undefined;
		state.report = {
			evaluation: "ev_1",
			arms: [{ label: "A", agent: "codex", version: 2, model: "gpt-x" }],
			capabilities: [
				{ capability: "terminal", benchmarks: ["hd-harbor-tb2"], arms: { A: { score: 0.41, ci_low: 0.3, ci_high: 0.52, runs: 30 } } },
			],
			benchmarks: [],
			reliability: [],
			models: ["gpt-x"],
		};
		const html = renderToStaticMarkup(<ReportCardTab agentId="codex" />);
		expect(html).toContain('data-testid="report-card"');
		expect(html).toContain('value="gpt-x"');
		expect(html).toContain('data-testid="capability-terminal"');
		expect(html).toContain('value="2"');
		// The Model filter lists only the models this agent's evaluations used (N34).
		expect(html).not.toContain('value="claude-y"');
		// Where it failed: the patterns seen, with example runs; unseen patterns are left out.
		expect(html).toContain('data-testid="report-failures"');
		expect(html).toContain("Loops");
		expect(html).toContain("3 of 10 runs");
		expect(html).toContain('data-testid="failure-example-r1"');
		expect(html).not.toContain('data-testid="pattern-pattern_ignored_error"');
	});

	it("helpers", () => {
		expect(isEmptyReport({ evaluation: "", arms: [], capabilities: [], benchmarks: [], reliability: [] }, undefined)).toBe(true);
		expect(isEmptyReport(undefined, { status: 500 })).toBe(false);
		expect(modelOptions(["b", "a"], "")).toEqual(["b", "a"]);
		expect(modelOptions(["a"], "gpt-6-luna")).toEqual(["gpt-6-luna", "a"]);
		expect(modelOptions(undefined, "a")).toEqual(["a"]);
		expect(agentReportCardRequest({ agent: "my agent" })).toEqual({ url: "/agents/my%20agent/report-card" });
		expect(agentReportCardRequest({ agent: "x", version: 2, model: "m" })).toEqual({
			url: "/agents/x/report-card",
			params: { version: "2", model: "m" },
		});
	});

	it("reads version and model from the address and names its selects (N54, N55)", () => {
		state.versions = [v(1), v(2)];
		state.reportError = undefined;
		state.reportArgs = [];
		state.search = { tab: "report-card", version: "1", model: "gpt-6-luna" };
		state.report = { evaluation: "ev_1", arms: [], capabilities: [], benchmarks: [], reliability: [], models: ["gpt-x"] };
		const html = renderToStaticMarkup(<ReportCardTab agentId="codex" />);
		expect(state.reportArgs).toContainEqual({ agent: "codex", version: 1, model: "gpt-6-luna" });
		// A model from the link that the list lacks is still shown, and selected.
		expect(html).toMatch(/<option value="gpt-6-luna" selected="">gpt-6-luna<\/option>/);
		expect(html).toMatch(/<option value="1" selected="">v1/);
		expect(html).toContain('aria-label="Version"');
		expect(html).toContain('aria-label="Model"');
		expect(html).not.toContain('data-testid="agent-report-card-version-missing"');
		state.search = {};
	});

	it("says when the version in the address does not exist", () => {
		state.versions = [v(1), v(2)];
		state.reportArgs = [];
		state.search = { version: "9" };
		const html = renderToStaticMarkup(<ReportCardTab agentId="codex" />);
		expect(html).toContain("Version 9 of this agent was not found. Showing the latest version.");
		expect(state.reportArgs).toContainEqual({ agent: "codex", version: undefined, model: undefined });
		state.search = {};
	});
});