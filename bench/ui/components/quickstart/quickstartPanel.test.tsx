import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MINED_BY } from "./quickstart.utils";
import { suite, task } from "./quickstart.utils.test";
import { QuickstartBody } from "./quickstartPanel";

describe("QuickstartBody", () => {
	it("shows the four commands with their cost", () => {
		const html = renderToStaticMarkup(<QuickstartBody suites={[]} />);
		for (const key of ["init", "mine", "compare", "ci"]) {
			expect(html).toContain(`data-testid="quickstart-step-${key}"`);
		}
		expect(html).toContain("heimdall tasks mine -limit 10");
		expect(html).toContain("$0, Docker only");
		expect(html).toContain('data-testid="quickstart-no-mined"');
	});

	it("lists unreviewed mined tasks with the review command", () => {
		const html = renderToStaticMarkup(
			<QuickstartBody
				suites={[
					suite("minitext", [
						task("m-1", { created_by: MINED_BY, instruction_reviewed: false }),
						task("m-2", { created_by: MINED_BY, instruction_reviewed: true }),
					]),
					suite("other", [task("c01", null)]),
				]}
			/>,
		);
		expect(html).toContain('data-testid="quickstart-row-minitext"');
		expect(html).not.toContain('data-testid="quickstart-row-other"');
		expect(html).toContain('data-testid="quickstart-unreviewed-m-1"');
		expect(html).not.toContain('data-testid="quickstart-unreviewed-m-2"');
		expect(html).toContain("heimdall task review minitext/m-1");
		expect(html).toContain("1 task has an unreviewed instruction.");
	});
});
