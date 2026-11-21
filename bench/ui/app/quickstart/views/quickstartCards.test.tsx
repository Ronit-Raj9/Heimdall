import "@/lib/i18n";

import { MINED_BY } from "@/components/quickstart/quickstart.utils";
import { suite, task } from "@/components/quickstart/quickstart.utils.test";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MinedTasks, QuickstartSteps } from "./quickstartCards";

describe("Quickstart step cards", () => {
	it("shows the four commands as numbered cards with a cost pill", () => {
		const html = renderToStaticMarkup(<QuickstartSteps />);
		for (const key of ["init", "mine", "compare", "ci"]) expect(html).toContain(`data-testid="quickstart-step-${key}"`);
		expect(html).toContain("heimdall tasks mine -limit 10");
		expect(html).toContain("Step 4");
		expect(html).toContain("Forecast first");
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
				]}
			/>,
		);
		expect(html).toContain('data-testid="quickstart-row-minitext"');
		expect(html).not.toContain('data-testid="quickstart-row-other"');
		expect(html).toContain("heimdall task review minitext/m-1");
		expect(html).toContain("1 needs review");
	});
});
