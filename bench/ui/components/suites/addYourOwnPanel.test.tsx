import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ADD_STEPS, ADD_TABS } from "./addYourOwn.steps";
import { AddYourOwnPanel } from "./addYourOwnPanel";
import { CopyCodeBlock } from "./copyCodeBlock";

// HTML-escapes a command the way React renders text.
const escape = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

describe("AddYourOwnPanel", () => {
	it("shows three tabs and the hidden-material note", () => {
		const html = renderToStaticMarkup(<AddYourOwnPanel />);
		for (const tab of ADD_TABS) expect(html).toContain(`data-testid="suites-add-tab-${tab}"`);
		expect(html).toContain(">Task from a commit<");
		expect(html).toContain(">New benchmark in another language<");
		expect(html).toContain(">Your own agent<");
		expect(html).toContain("Hidden tests, solutions and controls never appear here.");
	});

	for (const tab of ADD_TABS) {
		it(`lists every ${tab} step as a copyable block with its explanation`, () => {
			const html = renderToStaticMarkup(<AddYourOwnPanel defaultTab={tab} />);
			for (const step of ADD_STEPS[tab]) {
				expect(html).toContain(`data-testid="suites-add-${tab}-${step.key}"`);
				expect(html).toContain(escape(step.code).replace(/'/g, "&#x27;"));
			}
		});
	}

	it("uses the contract's commands", () => {
		const all = Object.values(ADD_STEPS)
			.flat()
			.map((step) => step.code)
			.join("\n");
		for (const command of [
			"task init -suite",
			"task validate",
			"task admit",
			"suite init",
			"suite lock",
			"suite admit",
			"agent init",
			"agent build",
			"agent check",
		]) {
			expect(all).toContain(`heimdall ${command}`);
		}
	});
});

describe("CopyCodeBlock", () => {
	it("has a labelled copy button and an always-present polite live region", () => {
		const html = renderToStaticMarkup(<CopyCodeBlock code="bifrost-bench task admit <suite>/<task>" label="Admit the task" testId="b" />);
		expect(html).toContain('aria-label="Copy command: Admit the task"');
		expect(html).toMatch(/<span class="sr-only" role="status" aria-live="polite" data-testid="b-status"><\/span>/);
		expect(html).toContain("bifrost-bench task admit &lt;suite&gt;/&lt;task&gt;");
	});
});