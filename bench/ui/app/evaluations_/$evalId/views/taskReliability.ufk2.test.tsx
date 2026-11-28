import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { RunListRow } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { taskReliability, TaskReliabilityView } from "./taskReliability";

// Rows as GET /runs?exp=<id> answers them (handlers/runlist.go).
const row = (task: string, trial: number, outcome: string, sealed = true, condition = "clean") =>
	({ run_id: `r_${task}_${trial}`, exp_id: "e1", pair_id: "", suite: "s", task, arm: "A", condition, trial, attempt: 1, counted: true, sealed, outcome }) as unknown as RunListRow;

describe("taskReliability (K2)", () => {
	it("counts mixed tries per task while some are still running", () => {
		const rows = [
			row("t1", 1, "pass"),
			row("t1", 2, "pass"),
			row("t1", 3, "pass"),
			row("t2", 1, "pass"),
			row("t2", 2, "fail"),
			row("t2", 3, "unknown", false),
			row("t3", 1, "fail"),
			row("t4", 1, "unknown", false),
			row("t1", 1, "fail", true, "tool_broken"),
		];
		const rel = taskReliability(rows, 3);
		expect(rel).toMatchObject({ always: 1, some: 1, never: 1 });
		expect(rel.tasks.find((e) => e.task === "t2")).toMatchObject({ solved: 1, done: 2, left: 1 });
		const html = render(<TaskReliabilityView rel={rel} active multiArm={false} testId="rel" />);
		expect(html).toContain("So far, from finished tries. Solved every time: 1 of 3 tasks; sometimes: 1; never: 1");
		expect(html).toContain("solved 1 of 2, 1 to go");
		expect(html).toContain("3 tries to go");
	});
	it("final: headline without the live words", () => {
		const rows = ["t1", "t2", "t3", "t4", "t5"].flatMap((t, i) => [1, 2, 3].map((k) => row(t, k, i < 3 || (i === 3 && k === 1) ? "pass" : "fail")));
		const html = render(<TaskReliabilityView rel={taskReliability(rows, 3)} active={false} multiArm={false} testId="rel" />);
		expect(html).toContain("Solved every time: 3 of 5 tasks; sometimes: 1; never: 1");
		expect(html).toContain("solved 3 of 3");
		expect(html).not.toContain("So far");
	});
});
