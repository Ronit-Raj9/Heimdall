import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { RoundView } from "@/lib/types/improve";
import { describe, expect, it } from "vitest";
import { IMPROVEMENT_FIXTURE } from "./improve.fixtures";
import { RoundTimeline } from "./roundTimeline";

const round: RoundView = {
	...IMPROVEMENT_FIXTURE.round_views[0],
	n: 2,
	candidates: [
		{ id: "c1", slot: 1, kind: "mutation", source: "agents/coder.yaml@v3", dev_diff: 0.1, dev_tasks: 24 },
		{ id: "c2", slot: 2, kind: "merge", dropped: "behind after the first half", dev_diff: -0.05 },
		{ id: "c3", slot: 3, kind: "restart" },
		{ id: "c4", slot: 4, kind: "mutation", source: "agents/coder.yaml@v3" },
	],
	winner: "c1",
	commit: { p: 0.02, level: 0.033, mean: 0.2, n: 18, n_nonzero: 7, missing: 0, skipped: false, why: "" },
};

describe("RoundTimeline", () => {
	it("writes one row per round and each idea with its source, practice difference and why it stopped", () => {
		const html = render(<RoundTimeline rounds={[round]} />);
		expect(html).toContain("Round 2: 4 ideas tried, 1 tested on the gate, kept (p 0.02).");
		expect(html).toContain("edit of v3");
		expect(html).toContain("merge");
		expect(html).toContain("restart from version 0");
		expect(html).toContain("practice difference +10.0 pts");
		expect(html).toContain("stopped: behind after the first half");
	});

	it("reads an older round with no ideas list as one idea", () => {
		const html = render(<RoundTimeline rounds={[IMPROVEMENT_FIXTURE.round_views[1]]} />);
		expect(html).toContain("Round 2: 1 ideas tried, 1 tested on the gate, not kept.");
	});
});