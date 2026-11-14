import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { LeaderboardFilters, type LeaderboardFiltersProps } from "./leaderboardFilters";

const noop = () => undefined;
const props: LeaderboardFiltersProps = {
	suite: "coding-core@1.0.0",
	suites: ["coding-core@1.0.0", "coding-core@0.9.0"],
	condition: "clean",
	conditions: ["clean", "model-outage"],
	scope: "all",
	synthetic: "exclude",
	onSuite: noop,
	onCondition: noop,
	onScope: noop,
	onSynthetic: noop,
};

describe("LeaderboardFilters", () => {
	it("shows the board's suite and condition and presses its scope and synthetic values", () => {
		const html = renderToStaticMarkup(<LeaderboardFilters {...props} />);
		expect(html).toContain('data-testid="leaderboard-suite-picker"');
		expect(html).toContain('data-testid="leaderboard-condition-picker"');
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="leaderboard-scope-all"/);
		expect(html).toMatch(/aria-pressed="false"[^>]*data-testid="leaderboard-scope-common"/);
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="leaderboard-synthetic-exclude"/);
		expect(html).toMatch(/aria-pressed="false"[^>]*data-testid="leaderboard-synthetic-only"/);
		expect(html).toContain('title="Every entry over the tasks all of them ran."');
	});

	it("labels every control", () => {
		const html = renderToStaticMarkup(<LeaderboardFilters {...props} />);
		for (const label of [">Suite<", ">Condition<", ">Tasks<", ">Runs<"]) expect(html).toContain(label);
		expect(html).toContain('aria-label="Board filters"');
	});

	it("disables the pickers when there is nothing to pick", () => {
		const html = renderToStaticMarkup(<LeaderboardFilters {...props} suite="" suites={[]} condition="" conditions={[]} />);
		expect(html).toMatch(/data-testid="leaderboard-suite-picker"[^>]*disabled|disabled[^>]*data-testid="leaderboard-suite-picker"/);
	});
});