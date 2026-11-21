import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import i18n from "@/lib/i18n";
import { UNKNOWN_TRACK } from "@/components/scenario/scenario.utils";
import { ALL_TRACKS, LeaderboardFilters, trackLabel, type LeaderboardFiltersProps } from "./leaderboardFilters";

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
		for (const label of [">Benchmark<", ">Failure test<", ">Tasks<", ">Runs<"]) expect(html).toContain(label);
		expect(html).toContain('aria-label="Board filters"');
	});

	it("disables the pickers when there is nothing to pick", () => {
		const html = renderToStaticMarkup(<LeaderboardFilters {...props} suite="" suites={[]} condition="" conditions={[]} />);
		expect(html).toMatch(/data-testid="leaderboard-suite-picker"[^>]*disabled|disabled[^>]*data-testid="leaderboard-suite-picker"/);
	});
});
describe("LeaderboardFilters track filter", () => {
	const trackGroups = [
		{ track: "coding", suites: ["coding-core@1.0.0", "coding-core@0.9.0"] },
		{ track: "memory", suites: ["memory-recall@0.1.0"] },
	];

	it("sits over the suite picker when suites are grouped by track", () => {
		const html = renderToStaticMarkup(<LeaderboardFilters {...props} trackGroups={trackGroups} track={null} onTrack={noop} />);
		expect(html).toContain('data-testid="leaderboard-track-picker"');
		expect(html).toContain(">Track<");
		expect(html.indexOf("leaderboard-track-picker")).toBeLessThan(html.indexOf("leaderboard-suite-picker"));
		// A labelled control: the label points at the trigger.
		expect(html).toMatch(/<label for="([^"]+)"[^>]*>Track<\/label><button[^>]*id="\1"/);
	});

	it("is left out without groups, or when no suite's track is known", () => {
		expect(renderToStaticMarkup(<LeaderboardFilters {...props} />)).not.toContain("leaderboard-track-picker");
		const unknown = [{ track: UNKNOWN_TRACK, suites: ["coding-core@1.0.0"] }];
		expect(renderToStaticMarkup(<LeaderboardFilters {...props} trackGroups={unknown} onTrack={noop} />)).not.toContain(
			"leaderboard-track-picker",
		);
	});

	it("labels tracks, suites not on disk and newer tracks", () => {
		const t = i18n.getFixedT("en", "bench");
		expect(trackLabel("tool_use", t)).toBe("Tool use");
		expect(trackLabel("browser", t)).toBe("Browser");
		expect(trackLabel("computer_use", t)).toBe("Computer use");
		expect(trackLabel("artifact", t)).toBe("Artifacts");
		expect(trackLabel(UNKNOWN_TRACK, t)).toBe("Not on disk");
		expect(trackLabel("robotics", t)).toBe("robotics");
		expect(ALL_TRACKS).not.toBe("");
	});
});