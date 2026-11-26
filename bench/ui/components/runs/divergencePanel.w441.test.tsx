import "@/lib/i18n";

import type { PairDivergenceResponse } from "@/lib/store/apis/behaviourApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { DivergenceView } from "./divergencePanel";

const side = { steps: 3, turns: 2, retries: 0, test_runs: 1, edits: 0, errors: 1 };

const base: PairDivergenceResponse = {
	pair_id: "p1",
	arms: ["base", "cand"],
	runs: ["r1", "r2"],
	outcomes: ["pass", "fail"],
	available: true,
	divergence: {
		found: true,
		category: "test_run",
		seq_a: 4,
		seq_b: 4,
		text_a: "pytest -q",
		text_b: "write tests/test_x.py",
		after: [{ ...side, cost_usd: 0.25 }, side],
		note: "n",
	},
};

describe("first divergence panel", () => {
	it("names the point, both sides, the counts after it and the checked results", () => {
		const html = renderToStaticMarkup(<DivergenceView data={base} />);
		expect(html).toContain("Where the runs first differed");
		expect(html).toContain("ran the tests here, the other did not");
		expect(html).toContain("base, step 4: pytest -q");
		expect(html).toContain("cand, step 4: write tests/test_x.py");
		expect(html).toContain("After this point");
		expect(html).toContain("Model turns");
		expect(html).toContain("$0.25");
		expect(html).toContain("not known");
		expect(html).toContain("Final result, checked by the task&#x27;s tests: base Passed, cand Failed.");
		expect(html).toContain("This is the first difference we saw, not a proven cause.");
	});

	it("says when the runs did the same steps", () => {
		const html = renderToStaticMarkup(<DivergenceView data={{ ...base, divergence: { found: false, after: [side, side], note: "n" } }} />);
		expect(html).toContain("The two runs did the same steps.");
	});

	it("shows the reason when a run has no step timeline", () => {
		const reason = "Run r1 was recorded before step timelines existed.";
		const html = renderToStaticMarkup(<DivergenceView data={{ ...base, available: false, divergence: undefined, reason }} />);
		expect(html).toContain(reason);
		expect(html).not.toContain("After this point");
	});
});