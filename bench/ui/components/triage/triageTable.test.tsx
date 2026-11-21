import "@/lib/i18n";

import type { Triage } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TriageBanner } from "./triageBanner";
import { TriageTable } from "./triageTable";

const triage = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/experiments.triage.json"), "utf8")) as Triage;

describe("TriageTable", () => {
	const render = (selected: string | null = null) =>
		renderToStaticMarkup(<TriageTable groups={triage.groups} arms={["A", "B"]} selected={selected} onSelect={() => undefined} />);

	it("shows patterns by arm with counts, shares and a B - A column", () => {
		const html = render();
		expect(html).toContain('data-testid="triage-table"');
		for (const g of triage.groups) expect(html).toContain(`data-testid="triage-row-${g.pattern}"`);
		expect(html).toContain(">Setup A<");
		expect(html).toContain(">B - A<");
		// Loop: A 3/24 (12.5%), B 5/23 (21.7%) with 1 unknown apart; B - A = +9.2 points.
		expect(html).toMatch(/data-testid="triage-cell-pattern_loop-A".*?>3\/24<.*?>12\.5%</);
		expect(html).toMatch(/data-testid="triage-cell-pattern_loop-B".*?>5\/23<.*?>21\.7%<.*?\+1 not checked/);
		expect(html).toMatch(/data-testid="triage-diff-pattern_loop".*?>\+9\.2 pts</);
		expect(html).toMatch(/data-testid="triage-diff-pattern_unverified_finish".*?>-13\.2 pts</);
	});

	it("has no p-value and marks the selected row", () => {
		const html = render("pattern_loop");
		expect(html).not.toMatch(/p = |p-value:|p &lt;/);
		expect(html).toMatch(/data-testid="triage-row-pattern_loop" data-state="selected"/);
		expect(html).toMatch(/aria-expanded="true"[^>]*data-testid="triage-show-pattern_loop"/);
		expect(html).toMatch(/aria-expanded="false"[^>]*data-testid="triage-show-false_completion"/);
	});

	it("says not observed, hatched, instead of 0% when an arm observed nothing", () => {
		const groups = [{ ...triage.groups[0], per_arm: { A: { num: 0, den: 0, unknown: 6 }, B: { num: 1, den: 4, unknown: 0 } } }];
		const html = renderToStaticMarkup(<TriageTable groups={groups} arms={["A", "B"]} selected={null} onSelect={() => undefined} />);
		expect(html).toContain("not checked");
		expect(html).toContain("+6 not checked");
		expect(html).toContain("bench-hatched");
		expect(html).toMatch(/data-testid="triage-diff-pattern_loop".*?>not known</);
		expect(html).not.toContain(">0%<");
	});
});

describe("TriageBanner", () => {
	it("says patterns are observed, not root causes", () => {
		const html = renderToStaticMarkup(<TriageBanner />);
		expect(html).toContain('data-testid="triage-banner"');
		expect(html).toContain("These patterns show how runs failed, not why.");
	});
});