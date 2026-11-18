import "@/lib/i18n";

import { experimentExportPath, experimentExportUrl, parseJSONLines, suiteHarborUrl } from "@/lib/store/apis/exportApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ExportMenu, SuiteExportMenu } from "./exportMenu";

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
		expect(html).toContain("6 sealed runs");
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

describe("SuiteExportMenu", () => {
	it("offers the withheld Harbor tar and names the CLI for a runnable export", () => {
		const html = renderToStaticMarkup(<SuiteExportMenu suiteId="coding-core" />);
		expect(html).toContain('href="/api/bench/suites/coding-core/export?format=harbor"');
		expect(html).toContain('download="coding-core.harbor.tar.gz"');
		expect(html).toContain("withheld");
		expect(html).toContain("-include-hidden");
	});
});