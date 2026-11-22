import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import i18n from "i18next";
import { describe, expect, it } from "vitest";
import { checkLabel, ReadinessResults, readinessReport } from "./admissionResults";

const local = {
	suite: "faq",
	problems: [{ path: "tasks/b/task.yaml", severity: "warning", message: "controls.mutants: empty" }],
	order: ["a", "b"],
	failed: 1,
	tasks: {
		a: { admitted: true, checks: [{ name: "reference_0", want: "pass", got: "pass", passed: true }, { name: "wrong_answer_1", want: "fail", got: "fail", passed: true }] },
		b: { admitted: false, checks: [{ name: "exploit", want: "fail", got: "pass", passed: false, detail: "scored 1" }] },
	},
};

const hosted = {
	suite: "faq",
	exp_id: "exp1",
	tasks: [{ task: "a", oracle: "pass", noop: "fail", admitted: true }, { task: "b", oracle: "fail", noop: "fail", admitted: false, reason: "the right answer failed" }],
	admitted: 1,
	failed: 1,
};

describe("readinessReport", () => {
	it("reads local admission results in order", () => {
		const r = readinessReport(local)!;
		expect(r.tasks.map((x) => [x.task, x.ready])).toEqual([["a", true], ["b", false]]);
		expect(r.tasks[1].checks[0]).toMatchObject({ name: "exploit", passed: false, detail: "scored 1" });
		expect(r.problems).toHaveLength(1);
	});
	it("reads hosted admission results as two checks", () => {
		const r = readinessReport(hosted)!;
		expect(r.tasks[1]).toMatchObject({ task: "b", ready: false, reason: "the right answer failed" });
		expect(r.tasks[1].checks.map((c) => c.passed)).toEqual([false, true]);
	});
	it("reads every named check of a full hosted readiness run", () => {
		const r = readinessReport({
			suite: "s",
			tasks: [
				{
					task: "a",
					oracle: "pass",
					noop: "fail",
					admitted: false,
					checks: [
						{ name: "reference_0", want: "pass", got: "pass", passed: true },
						{ name: "wrong_answer_1", want: "fail", got: "fail", passed: true },
						{ name: "reference_stale_notes", want: "pass", got: "not_run", passed: false, detail: "checked on your own machine" },
					],
				},
			],
		})!;
		expect(r.tasks[0].checks.map((c) => c.name)).toEqual(["reference_0", "wrong_answer_1", "reference_stale_notes"]);
		const html = render(<ReadinessResults report={r} />);
		expect(html).toContain("Wrong answer 1 is caught");
		expect(html).toContain("not run on this worker");
		expect(html).not.toContain("got not_run");
	});
	it("returns null for other results", () => {
		expect(readinessReport(undefined)).toBeNull();
		expect(readinessReport({ exp_id: "x" })).toBeNull();
	});
});

describe("ReadinessResults", () => {
	it("names checks in plain words and opens the failing task", () => {
		const t = i18n.getFixedT("en", "suiteBuild");
		expect(checkLabel("reference_0", t as never)).toBe("The right answer passes (try 1)");
		expect(checkLabel("wrong_answer_2", t as never)).toBe("Wrong answer 2 is caught");
		expect(checkLabel("noop_control", t as never)).toBe("An empty answer fails");
		const html = render(<ReadinessResults report={readinessReport(local)!} />);
		expect(html).toContain("1 of 2 tasks passed.");
		expect(html).toContain("A gaming attempt scores nothing");
		expect(html).toContain("got pass, wanted fail");
		expect(html).toMatch(/data-testid="readiness-task-b"><details open=""/);
		expect(html.replace(/<[^>]+>/g, " ")).not.toMatch(/oracle|noop|exploit/i);
	});
});
