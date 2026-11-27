import "@/lib/i18n";

import type { Grade, Run } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ClaimVsResult } from "./claimVsResult";

const grade = (over: Partial<Grade>): Grade => ({
	passed: true,
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

const run = { run_id: "r_1", outcome: "solved" } as unknown as Run;
const html = (g: Grade) => renderToStaticMarkup(<ClaimVsResult rows={[{ arm: "A", run, grade: g, gradeState: "ready" }]} />);

describe("solved with no hidden check (M4-55)", () => {
	it("says the reference answer passed it when no check ran and no reason is given", () => {
		expect(html(grade({}))).toContain('data-testid="grade-no-checks"');
	});

	it("says nothing extra when checks ran, a reason is given, or a judge decided", () => {
		expect(html(grade({ tests_ran: 3 }))).not.toContain("grade-no-checks");
		expect(html(grade({ reason: "matched" }))).not.toContain("grade-no-checks");
		expect(html(grade({ judged: true }))).not.toContain("grade-no-checks");
		expect(html(grade({ passed: false }))).not.toContain("grade-no-checks");
	});
});
