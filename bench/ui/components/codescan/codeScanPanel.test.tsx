import "@/lib/i18n";

import type { CodeScanReport } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CodeScanPanel, RunCodeScanPanel } from "./codeScanPanel";

const query = vi.hoisted(() => ({ scan: {} as Record<string, unknown>, diff: {} as Record<string, unknown>, args: [] as unknown[] }));

vi.mock("@/lib/store/apis/codescanApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/codescanApi")>();
	return {
		...actual,
		useGetRunCodeScanQuery: (arg: unknown) => {
			query.args.push(arg);
			return query.scan;
		},
	};
});

vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/benchApi")>();
	return { ...actual, useGetRunDiffQuery: () => query.diff };
});

const PATCH = [
	"diff --git a/app/run.py b/app/run.py",
	"new file mode 100644",
	"index 0000000..1111111",
	"--- /dev/null",
	"+++ b/app/run.py",
	"@@ -0,0 +1,3 @@",
	"+import os",
	"+def run(name):",
	'+    os.system("ls " + name) # <script>alert(1)</script>',
	"",
].join("\n");

const report: CodeScanReport = {
	run_id: "r_1",
	sealed: true,
	veto_at: "high",
	counted: 1,
	vetoed: true,
	scan: {
		rules_version: "1.0.0",
		scanned: true,
		files: 1,
		added_lines: 3,
		findings: [
			{
				rule: "shell-injection-python",
				title: "Shell command built from non-constant input (Python)",
				cwe: "CWE-78",
				severity: "high",
				file: "app/run.py",
				line: 3,
				patch_line: 9,
				snippet: 'os.system("ls " + name) # <script>alert(1)</script>',
				source: "builtin",
			},
		],
		external: [{ scanner: "bandit", image: "bench/x:1", exit_code: -1, results: 0, on_added_lines: 0, error: "timed out" }],
	},
};

const idle = { isLoading: false, isFetching: false, error: undefined, data: undefined, refetch: () => undefined };

beforeEach(() => {
	query.scan = idle;
	query.diff = idle;
	query.args = [];
});

describe("RunCodeScanPanel", () => {
	it("shows a skeleton while loading", () => {
		query.scan = { ...idle, isLoading: true };
		const html = renderToStaticMarkup(<RunCodeScanPanel runId="r_1" arm="A" />);
		expect(html).toContain('data-testid="codescan-A-loading"');
		expect(query.args).toEqual(["r_1"]);
	});

	it("shows the error with a retry", () => {
		query.scan = { ...idle, error: { status: 500, data: { error: "boom" } } };
		const html = renderToStaticMarkup(<RunCodeScanPanel runId="r_1" arm="B" />);
		expect(html).toContain('data-testid="codescan-B-error"');
		expect(html).toContain("Could not load the run&#x27;s code scan.");
		expect(html).toContain('data-testid="codescan-B-error-retry"');
	});

	it("renders the findings once loaded, with the diff when it loads", () => {
		query.scan = { ...idle, data: report };
		query.diff = { ...idle, data: { run_id: "r_1", diff: { path: "workspace.diff", sha256: "x", files: [] }, patch: PATCH } };
		const html = renderToStaticMarkup(<RunCodeScanPanel runId="r_1" arm="A" />);
		expect(html).toContain('data-testid="codescan-A-flagged-line"');
	});
});

describe("CodeScanPanel", () => {
	it("says the run is vetoed, lists the finding and marks its line in the diff", () => {
		const html = renderToStaticMarkup(<CodeScanPanel report={report} patch={PATCH} arm="A" />);
		expect(html).toContain("Vetoes the run (veto at high)");
		expect(html).toContain('data-testid="codescan-A-row"');
		expect(html).toContain("shell-injection-python");
		expect(html).toContain("(CWE-78)");
		expect(html).toContain("app/run.py:3");
		expect(html).toContain('data-patch-line="9"');
		expect(html).toContain("^ high shell-injection-python CWE-78");
		expect(html).toContain("From saved scan evidence.");
		expect(html).toContain("bandit failed: timed out. Its findings are unknown.");
		// Run text is inert: the markup in the snippet is escaped, never rendered.
		expect(html).not.toContain("<script>");
		expect(html).toContain("&lt;script&gt;");
	});

	it("keeps the table when the diff could not be loaded", () => {
		const html = renderToStaticMarkup(<CodeScanPanel report={report} arm="B" />);
		expect(html).toContain('data-testid="codescan-B-row"');
		expect(html).toContain('data-testid="codescan-B-nodiff"');
		expect(html).not.toContain("codescan-B-flagged-line");
	});

	it("shows a clean run and an unscanned run honestly", () => {
		const clean: CodeScanReport = {
			...report,
			sealed: false,
			vetoed: false,
			counted: 0,
			scan: { ...report.scan, findings: [], external: [] },
		};
		let html = renderToStaticMarkup(<CodeScanPanel report={clean} patch={PATCH} arm="A" />);
		expect(html).toContain("No findings");
		expect(html).toContain('data-testid="codescan-A-empty"');
		expect(html).toContain("Computed now from the saved diff.");
		expect(html).toContain("Comparison veto level high: not reached.");
		const unscanned: CodeScanReport = { ...clean, scan: { ...clean.scan, scanned: false, reason: "no exported diff" } };
		html = renderToStaticMarkup(<CodeScanPanel report={unscanned} arm="A" />);
		expect(html).toContain("Not scanned: no exported diff.");
	});

	it("says a scanner failed even when nothing was found", () => {
		const partial: CodeScanReport = { ...report, vetoed: false, counted: 0, scan: { ...report.scan, findings: [] } };
		const html = renderToStaticMarkup(<CodeScanPanel report={partial} patch={PATCH} arm="A" />);
		expect(html).toContain("No findings, but a scanner failed");
	});
});