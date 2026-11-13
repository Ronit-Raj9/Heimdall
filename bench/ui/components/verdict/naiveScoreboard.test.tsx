import "@/lib/i18n";

import type { Ratio } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { NaiveScoreboard } from "./naiveScoreboard";
import { verdictFixture } from "./testFixtures";

const ratio = (num: number, den: number, unknown = 0): Ratio => ({ num, den, unknown });

describe("NaiveScoreboard", () => {
	it("renders the fixture as a typical dashboard would: raw passes and B wins by N", () => {
		const html = renderToStaticMarkup(<NaiveScoreboard naive={verdictFixture.naive} arms={["A", "B"]} />);
		expect(html).toContain('data-testid="verdict-naive-scoreboard"');
		expect(html).toContain(">13/24<");
		expect(html).toContain(">19/24<");
		expect(html).toContain("&quot;B wins by 6&quot;");
		expect(html).toContain("Not the release decision.");
	});

	const cases = [
		{ name: "A ahead", naive: { A: ratio(19, 24), B: ratio(11, 24) }, headline: "&quot;A wins by 8&quot;" },
		{ name: "tie", naive: { A: ratio(12, 24), B: ratio(12, 24) }, headline: "&quot;Tie&quot;" },
	];
	for (const c of cases) {
		it(c.name, () => {
			const html = renderToStaticMarkup(<NaiveScoreboard naive={c.naive} arms={["A", "B"]} />);
			expect(html).toContain(c.headline);
		});
	}

	it("shows unknowns apart, never folded into the count", () => {
		const html = renderToStaticMarkup(<NaiveScoreboard naive={{ A: ratio(10, 24, 2), B: ratio(12, 24) }} arms={["A", "B"]} />);
		expect(html).toContain(">10/24");
		expect(html).toContain("+2 unknown");
		expect(html).toContain("bench-hatched");
	});
});