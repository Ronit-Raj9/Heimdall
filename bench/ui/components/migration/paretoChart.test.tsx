import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PARETO_FIXTURE } from "./migration.fixtures";
import { ParetoChart } from "./paretoChart";

describe("ParetoChart", () => {
	const html = renderToStaticMarkup(<ParetoChart pareto={PARETO_FIXTURE} />);

	it("is an image with a title and the frontier in words", () => {
		expect(html).toMatch(/<svg role="img" aria-labelledby="[^"]+ [^"]+"/);
		expect(html).toContain("Cost against verified success: 2 of 2 configs placed; x is Cost per verified success");
		expect(html).toMatch(/On the frontier: [^<]*custom-python/);
	});

	it("fills the frontier point and greys the dominated one, and says so in text", () => {
		expect(html.match(/data-testid="pareto-chart-mark" data-frontier="true"/g)).toHaveLength(1);
		expect(html.match(/data-testid="pareto-chart-mark" data-frontier="false"/g)).toHaveLength(1);
		expect(html).toContain("dominated by B");
		expect(html).toContain("Hollow, grey: dominated");
	});

	it("lists a point it cannot place with the reason, and draws no mark for it", () => {
		const pareto = {
			...PARETO_FIXTURE,
			points: [
				...PARETO_FIXTURE.points,
				{
					...PARETO_FIXTURE.points[0],
					id: "C",
					label: "C",
					placed: false,
					frontier: false,
					reason: "cost unknown: cost unknown in 1 of 2 runs",
				},
			],
		};
		const out = renderToStaticMarkup(<ParetoChart pareto={pareto} />);
		expect(out).toContain("not placed: cost unknown: cost unknown in 1 of 2 runs");
		expect(out.match(/data-testid="pareto-chart-mark"/g)).toHaveLength(2);
	});
});