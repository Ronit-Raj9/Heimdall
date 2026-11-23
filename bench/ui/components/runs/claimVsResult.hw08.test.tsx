import "@/lib/i18n";

import type { Grade, Run } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ClaimVsResult } from "./claimVsResult";

const grade = (over: Partial<Grade>): Grade => ({
	passed: false,
	invalid_evaluator: false,
	exit_code: 0,
	tests_ran: 0,
	failures: 0,
	errors: 0,
	skipped: 0,
	suite_version: "1",
	grader_version: "1",
	diff_sha256: "d",
	...over,
});

const run = { run_id: "r_48moiyu4c7", outcome: "invalid_evaluator" } as unknown as Run;

describe("run page grader line (fix wave 3, N273)", () => {
	it("says why the grader could not grade the run", () => {
		const why = "judge: the judge model gave no answer within 16 reply tokens (it spent them on reasoning).";
		const html = renderToStaticMarkup(
			<ClaimVsResult rows={[{ arm: "A", run, grade: grade({ invalid_evaluator: true, reason: why }), gradeState: "ready" }]} />,
		);
		expect(html).toContain(`The grader could not grade this run: ${why}`);
	});

	it("shows no grader line on a graded run", () => {
		const html = renderToStaticMarkup(
			<ClaimVsResult
				rows={[{ arm: "A", run, grade: grade({ passed: true, reason: "judge: correct by majority" }), gradeState: "ready" }]}
			/>,
		);
		expect(html).not.toContain("The grader could not grade this run");
	});
});