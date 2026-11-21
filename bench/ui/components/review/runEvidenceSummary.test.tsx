import "@/lib/i18n";

import type { RunDetail } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RunEvidenceSummary } from "./runEvidenceSummary";
import { runFixture } from "./testFixtures";

describe("RunEvidenceSummary", () => {
	it("shows the sealed outcome, the grade and the failed tests of a verifier-graded run", () => {
		const html = renderToStaticMarkup(<RunEvidenceSummary detail={runFixture} />);
		expect(html).toMatch(/data-testid="review-evidence-outcome"[^>]*data-outcome="fail"/);
		expect(html).toContain("failed");
		expect(html).toContain("tests/test_dates.p");
		expect(html).toContain("No AI grader");
	});

	it("shows judge votes, fired gates and the answer of a judged run", () => {
		const detail: RunDetail = {
			...runFixture,
			grade: {
				...runFixture.grade!,
				passed: false,
				mode: "answer",
				judged: true,
				judge: {
					model: "judge-model",
					prompt_version: "v1",
					prompt_sha256: "x",
					votes: ["correct", "incorrect", "unparsable"],
					usd: { state: "unknown", v: null },
				},
			},
			gates: [{ gate: "impossible_pass", version: "1.0.0", effect: "veto", fired: true, detail: "passed an impossible task" }],
			answer: { text: "<b>42</b>", source: "file", sha256: "s" },
		};
		const html = renderToStaticMarkup(<RunEvidenceSummary detail={detail} />);
		expect(html).toContain("judge-model");
		expect(html).toContain('data-testid="review-evidence-vote-2"');
		expect(html).toContain(">1/1/1<");
		expect(html).toContain("impossible_pass");
		expect(html).toContain("&lt;b&gt;42&lt;/b&gt;");
	});
});