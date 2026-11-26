import { render } from "@/lib/testing/render";
import type { SuiteSummary, SuiteTask } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { GUARD_CHECKS, GuardChecks, guardedTasks } from "./guardChecks";

const task = (id: string, failed: string[], guarded = true): SuiteTask =>
	({
		id,
		title: `Task ${id}`,
		checks: [
			{ name: "reference_1", control: "reference", want: "pass", got: "pass", passed: true },
			...(guarded
				? GUARD_CHECKS.map((name) => ({
						name,
						control: "other" as const,
						want: "pass",
						got: failed.includes(name) ? "fail" : "pass",
						passed: !failed.includes(name),
					}))
				: []),
		],
	}) as unknown as SuiteTask;

const suite = (tasks: SuiteTask[]) => ({ id: "s", tasks }) as unknown as SuiteSummary;

describe("GuardChecks", () => {
	it("is hidden when no task was guarded", () => {
		expect(render(<GuardChecks suite={suite([task("h", [], false)])} />)).toBe("");
	});

	it("shows the eight checks with marks and the not added line", () => {
		const s = suite([task("a", ["guard_no_answer_leak", "guard_privacy"]), task("b", []), task("h", [], false)]);
		expect(guardedTasks(s).map((t) => t.id)).toEqual(["a", "b"]);
		const html = render(<GuardChecks suite={s} />);
		expect(html).toContain("Quality checks for generated tasks");
		expect(html).toContain("Not added to the benchmark yet: 2 quality checks failed.");
		expect(html).not.toContain('data-testid="guard-not-added-b"');
		expect(html).toContain("The answer is not given away");
		expect(html).toContain("No personal data");
		expect(html.match(/data-testid="guard-check-a-/g)).toHaveLength(8);
		expect(html).not.toContain("guard-task-h");
		for (const w of ["—", "–", "heimdall", "bifrost-bench"]) expect(html).not.toContain(w);
	});

	it("says one check in the singular", () => {
		expect(render(<GuardChecks suite={suite([task("a", ["guard_privacy"])])} />)).toContain("1 quality check failed.");
	});
});