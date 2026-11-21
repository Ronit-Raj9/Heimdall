import "@/lib/i18n";

import glossary from "@/locales/en/glossary.json";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { GLOSSARY_KEYS, Term, termLabel, termTip } from "./term";

describe("termTip", () => {
	it("returns the glossary explanation", () => {
		expect(termTip("admitted")).toBe("Heimdall checked the task is fair: a correct solution passes and an agent that does nothing fails.");
		expect(termTip("canary")).toContain("re-run on a schedule");
	});
	it("returns empty for an unknown key", () => {
		expect(termTip("nope" as never)).toBe("");
	});
	it("every row has a label and a tip, and no em dashes", () => {
		expect(GLOSSARY_KEYS.length).toBeGreaterThan(40);
		for (const k of GLOSSARY_KEYS) {
			expect(termLabel(k), k).not.toBe("");
			expect(termTip(k), k).not.toBe("");
			expect(JSON.stringify(glossary.terms[k])).not.toMatch(/[–—]/);
		}
	});
});

describe("Term", () => {
	it("renders the label with a dotted underline, focusable, described by the tip", () => {
		const html = renderToStaticMarkup(<Term k="admitted">Ready</Term>);
		expect(html).toContain("Ready");
		expect(html).toContain("decoration-dotted");
		expect(html).toContain('tabindex="0"');
		expect(html).toContain('data-term="admitted"');
		const id = /aria-describedby="([^"]+)"/.exec(html)?.[1];
		expect(id).toBeTruthy();
		expect(html).toContain(`id="${id}"`);
		expect(html).toContain(termTip("admitted"));
	});
	it("defaults the label to the glossary's plain label", () => {
		expect(renderToStaticMarkup(<Term k="canary" />)).toContain("Drift monitor");
	});
});
