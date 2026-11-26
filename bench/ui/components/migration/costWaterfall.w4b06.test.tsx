import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { CostExplanation } from "@/lib/types/bench";
import { CostExplanationView } from "./costWaterfall";
import { COST_FIXTURE } from "./migration.fixtures";

const render = (e: CostExplanation) => renderToStaticMarkup(<CostExplanationView explanation={e} />);

describe("CostExplanationView model row", () => {
	it("shows the answered-as model on its own line", () => {
		const html = render(COST_FIXTURE);
		expect(html).not.toContain("gpt-5.4-mini (gpt-5.4-mini-2026-03-17)");
		expect(html).toMatch(/data-testid="cost-answered-as"[^>]*>Answered as gpt-5\.4-mini-2026-03-17</);
	});

	it("shows no second line when the configured model answered", () => {
		const e: CostExplanation = {
			...COST_FIXTURE,
			a: { ...COST_FIXTURE.a, served_models: [COST_FIXTURE.a.model] },
			b: { ...COST_FIXTURE.b, served_models: [] },
		};
		expect(render(e)).not.toContain("cost-answered-as");
	});
});