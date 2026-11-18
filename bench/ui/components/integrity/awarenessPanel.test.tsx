import "@/lib/i18n";

import type { AwarenessReport } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AwarenessPanel } from "./awarenessPanel";

const report: AwarenessReport = {
	exp_id: "e_x",
	realism: "standard",
	heuristic: true,
	scanner_version: "1.0.0",
	label: "Heuristic phrase scan.",
	per_arm: {
		B: { runs: 8, runs_scanned: 8, runs_with_signals: 1, signals: 2 },
		A: { runs: 8, runs_scanned: 7, runs_with_signals: 0, signals: 0 },
	},
	examples: [
		{
			run_id: "r_1",
			arm: "B",
			task: "t01",
			condition: "clean",
			source: "reasoning_summary",
			phrase: "this is a test",
			excerpt: "Maybe this is a test environment.",
		},
	],
	not_scanned: [{ run_id: "r_2", reason: "no gateway log" }],
};

describe("AwarenessPanel", () => {
	it("labels the scan as a heuristic, orders arms and marks the phrase", () => {
		const html = renderToStaticMarkup(<AwarenessPanel report={report} arms={["A", "B"]} />);
		expect(html).toContain('data-testid="awareness-heuristic"');
		expect(html.indexOf("awareness-arm-A")).toBeLessThan(html.indexOf("awareness-arm-B"));
		expect(html).toContain("<mark");
		expect(html).toContain("this is a test</mark>");
		expect(html).toContain("reasoning");
		expect(html).toContain("1 run could not be scanned");
	});
	it("says silence is not proof when nothing matched", () => {
		const html = renderToStaticMarkup(<AwarenessPanel report={{ ...report, examples: [], not_scanned: [] }} arms={["A", "B"]} />);
		expect(html).toContain('data-testid="awareness-no-examples"');
		expect(html).toContain("unverbalized");
		expect(html).not.toContain("awareness-not-scanned");
	});
});