import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(join(__dirname, "globals.css"), "utf8");

// Q-5: only prose links are underlined; breadcrumbs, sidebar items, card titles and list rows are not.
describe("global link underline rule", () => {
	const rules = [...css.matchAll(/([^{}]+)\{[^{}]*text-decoration-line:\s*underline/g)].map((m) => m[1].replace(/\/\*[\s\S]*?\*\//g, "").trim());
	it("has exactly one underline rule, scoped to prose", () => {
		expect(rules).toEqual([":where(p, .prose li) a:where(:not([data-slot], td *, th *))"]);
	});
	it("never underlines every link or every list item link", () => {
		for (const r of rules) {
			expect(r).not.toMatch(/(^|[,(]\s*)li[\s,)]/);
			expect(r).not.toMatch(/^a(\s|:|$)/);
		}
	});
});

describe("print stylesheet (Q-42, RK)", () => {
	const print = css.slice(css.indexOf("@media print"));
	it("hides the shell and controls and opens folded sections", () => {
		for (const part of ['[data-testid="bench-sidebar"]', '[data-testid="bench-header"]', '[role="tablist"]', '[role="menu"]', "button", '[data-testid="toaster"]']) {
			expect(print).toContain(part);
		}
		expect(print).toContain("details > *:not(summary)");
	});
	it("never hides tables or status pills", () => {
		const hidden = print.split("display: none")[0];
		expect(hidden).not.toMatch(/\btable\b|status-pill|statusPill/);
	});
});
