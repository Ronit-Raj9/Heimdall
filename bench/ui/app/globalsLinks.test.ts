import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(join(__dirname, "globals.css"), "utf8");

// Q-5: only prose links are underlined; breadcrumbs, sidebar items, card titles and list rows are not.
describe("global link underline rule", () => {
	const rules = [...css.matchAll(/([^{}]+)\{[^{}]*text-decoration-line:\s*underline/g)].map((m) => m[1].replace(/\/\*[\s\S]*?\*\//g, "").trim());
	it("has exactly one underline rule, scoped to prose", () => {
		expect(rules).toEqual([":where(p, dd, .prose li) a:where(:not([data-slot]))"]);
	});
	it("never underlines every link or every list item link", () => {
		for (const r of rules) {
			expect(r).not.toMatch(/(^|[,(]\s*)li[\s,)]/);
			expect(r).not.toMatch(/^a(\s|:|$)/);
		}
	});
});
