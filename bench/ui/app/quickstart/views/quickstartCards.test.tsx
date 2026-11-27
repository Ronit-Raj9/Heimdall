import "@/lib/i18n";

import { MINED_BY } from "@/components/quickstart/quickstart.utils";
import { suite, task } from "@/components/quickstart/quickstart.utils.test";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MinedTasks, QuickstartSteps } from "./quickstartCards";

describe("Quickstart step cards", () => {
	it("shows the install step and the four commands as numbered cards, with a cost note and no pills", () => {
		const html = renderToStaticMarkup(<QuickstartSteps />);
		for (const key of ["install", "init", "mine", "compare", "ci"]) expect(html).toContain(`data-testid="quickstart-step-${key}"`);
		expect(html).toContain("heimdall tasks mine --limit 10");
		expect(html).toContain("Step 0");
		expect(html).toContain("Step 4");
		expect(html).toContain("Estimates the cost before it runs");
		expect(html).not.toContain("rounded-full");
		expect(html).not.toContain("hover:shadow-md");
	});

	it("shows an empty state, then the suites whose mined tasks need review", () => {
		expect(renderToStaticMarkup(<MinedTasks suites={[]} />)).toContain('data-testid="quickstart-no-mined"');
		const html = renderToStaticMarkup(
			<MinedTasks
				suites={[
					suite("minitext", [
						task("m-1", { created_by: MINED_BY, instruction_reviewed: false }),
						task("m-2", { created_by: MINED_BY, instruction_reviewed: true }),
					]),
					suite("other", [task("c01", null)]),
					suite("zz-test-mined", [task("t-1", { created_by: MINED_BY, instruction_reviewed: false })]),
				]}
			/>,
		);
		expect(html).toContain('data-testid="quickstart-row-minitext"');
		expect(html).not.toContain('data-testid="quickstart-row-other"');
		expect(html).toContain("heimdall task review minitext/m-1");
		expect(html).toContain("1 needs review");
		expect(html).not.toContain("zz-test-mined");
		expect(html).not.toContain('data-testid="quickstart-unreviewed-total"');
	});
});
