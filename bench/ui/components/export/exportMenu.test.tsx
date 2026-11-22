import "@/lib/i18n";

import { experimentExportPath, experimentExportUrl, parseJSONLines, suiteHarborUrl } from "@/lib/store/apis/exportApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ExperimentExportMenu, ExportMenu, SuiteExportMenu } from "./exportMenu";

const runTable = vi.hoisted(() => ({ skip: [] as boolean[] }));
vi.mock("@/lib/store/apis/exportApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/exportApi")>()),
	useGetExperimentRunTableQuery: (_id: string, opts?: { skip?: boolean }) => {
		runTable.skip.push(Boolean(opts?.skip));
		return { data: [{ synthetic: false }, { synthetic: false }], error: undefined };
	},
}));

describe("export URLs", () => {
	it("escapes the id as one path segment and keeps the format in the query", () => {
		expect(experimentExportPath("e_1", "eee")).toBe("/experiments/e_1/export?format=eee");
		expect(experimentExportPath("a/b?c", "csv")).toBe("/experiments/a%2Fb%3Fc/export?format=csv");
		expect(experimentExportUrl("e_1", "eee", true)).toBe("/api/bench/experiments/e_1/export?format=eee&allow_synthetic=true");
		expect(suiteHarborUrl("coding-core")).toBe("/api/bench/suites/coding-core/export?format=harbor");
	});

	it("parses JSON lines and skips blank lines", () => {
		expect(parseJSONLines<{ a: number }>('{"a":1}\n\n{"a":2}\n')).toEqual([{ a: 1 }, { a: 2 }]);
		expect(() => parseJSONLines("{not json}\n")).toThrow();
	});
});

describe("ExportMenu", () => {
	it("offers EEE, JSONL and CSV downloads of the experiment and the trace CLI line", () => {
		const html = renderToStaticMarkup(<ExportMenu expId="e_smoke" runs={6} />);
		expect(html).toContain('data-testid="export-menu"');
		expect(html).toContain('href="/api/bench/experiments/e_smoke/export?format=eee"');
		expect(html).toContain('download="e_smoke.eee.jsonl"');
		expect(html).toContain('href="/api/bench/experiments/e_smoke/export?format=jsonl"');
		expect(html).toContain('href="/api/bench/experiments/e_smoke/export?format=csv"');
		expect(html).toContain("6 saved runs");
		expect(html).toContain("export otlp -exp e_smoke -to langfuse");
		expect(html).not.toContain("allow_synthetic");
	});

	it("asks for synthetic results labelled and says so", () => {
		const html = renderToStaticMarkup(<ExportMenu expId="e_smoke" synthetic />);
		expect(html).toContain("allow_synthetic=true");
		expect(html).toContain("labelled synthetic");
		expect(html).not.toContain("ml-2 font-normal");
	});
});

// 6-C-07: the EEE link waits for the server's synthetic classification and says when it failed.
describe("ExportMenu before classification", () => {
	it("offers no EEE link while the classification is unknown", () => {
		const html = renderToStaticMarkup(<ExportMenu expId="e_smoke" synthetic={null} />);
		expect(html).not.toContain("format=eee");
		expect(html).toContain('data-testid="export-eee-pending"');
		expect(html).toContain('href="/api/bench/experiments/e_smoke/export?format=jsonl"');
	});
	it("says the classification failed", () => {
		const html = renderToStaticMarkup(<ExportMenu expId="e_smoke" synthetic={null} classifyError={{ status: 500, data: {} }} />);
		expect(html).toContain('role="alert"');
		expect(html).not.toContain("format=eee");
	});
});

// 6-C-08: fixture mode has no server to download from, so no export menu is offered.
describe("fixture mode", () => {
	it("hides the suite export menu", () => {
		expect(renderToStaticMarkup(<SuiteExportMenu suiteId="coding-core" fixtureMode />)).toBe("");
	});
});

describe("SuiteExportMenu", () => {
	it("offers the withheld Harbor tar and names the CLI for a runnable export", () => {
		const html = renderToStaticMarkup(<SuiteExportMenu suiteId="coding-core" fixtureMode={false} />);
		expect(html).toContain('href="/api/bench/suites/coding-core/export?format=harbor"');
		expect(html).toContain('download="coding-core.harbor.tar.gz"');
		expect(html).toContain("withheld");
		expect(html).toContain("-include-hidden");
	});
});
// N70: the comparison's Export tab opens the menu at once, so the tab is never an empty disclosure.
describe("ExperimentExportMenu initiallyOpen", () => {
	it("renders open with the three downloads and reads the run table at once", () => {
		runTable.skip.length = 0;
		const html = renderToStaticMarkup(<ExperimentExportMenu expId="e_smoke" fixtureMode={false} initiallyOpen />);
		expect(html).toMatch(/<details[^>]*open=""/);
		expect(html).toContain('href="/api/bench/experiments/e_smoke/export?format=eee"');
		expect(html).toContain('href="/api/bench/experiments/e_smoke/export?format=jsonl"');
		expect(html).toContain('href="/api/bench/experiments/e_smoke/export?format=csv"');
		expect(runTable.skip).toEqual([false]);
	});

	it("starts closed by default", () => {
		const html = renderToStaticMarkup(<ExperimentExportMenu expId="e_smoke" fixtureMode={false} />);
		expect(html).not.toMatch(/<details[^>]*open=""/);
	});
});