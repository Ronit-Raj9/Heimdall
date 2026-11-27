import type { SuiteSummary, SuiteTask } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { MINED_BY, isUnreviewed, minedRows, reviewCommand, unreviewedTotal } from "./quickstart.utils";

/** A task with only the fields the first-workflow page reads; the rest are empty. */
export function task(id: string, provenance: SuiteTask["provenance"], admitted = true): SuiteTask {
	return {
		id,
		version: "1.0.0",
		title: `Task ${id}`,
		project: "p",
		kind: "bugfix",
		difficulty: "",
		capabilities: [],
		tags: [],
		provenance,
		status: admitted ? "admitted" : "draft",
		stale: false,
		admitted,
		admitted_at: null,
		checks: [],
		hidden_test_files: 1,
		required_tests: 1,
		protected_globs: 0,
		mutants: 0,
		agent_timeout_s: 900,
	} as SuiteTask;
}

export function suite(id: string, tasks: SuiteTask[]): SuiteSummary {
	return { id, tasks } as unknown as SuiteSummary;
}

const mined = (reviewed: boolean) => ({ created_by: MINED_BY, instruction_reviewed: reviewed });

describe("first workflow utils", () => {
	it("counts mined, admitted and unreviewed tasks per suite, mined suites first", () => {
		const rows = minedRows([
			suite("coding-core", [task("c01", { created_by: "task init" }), task("c02", null)]),
			suite("minitext", [task("m-1", mined(false)), task("m-2", mined(true)), task("m-3", mined(false), false)]),
		]);
		expect(rows.map((r) => r.suite)).toEqual(["minitext", "coding-core"]);
		expect(rows[0]).toMatchObject({ tasks: 3, mined: 3, admittedMined: 2 });
		expect(rows[0].unreviewed.map((t) => t.id)).toEqual(["m-1", "m-3"]);
		expect(rows[1]).toMatchObject({ mined: 0, unreviewed: [] });
		expect(unreviewedTotal(rows)).toBe(2);
	});

	it("treats a missing field as not drafted by a tool", () => {
		expect(isUnreviewed(task("a", null))).toBe(false);
		expect(isUnreviewed(task("a", { created_by: MINED_BY }))).toBe(false);
		expect(isUnreviewed(task("a", mined(false)))).toBe(true);
	});

	it("builds the review command", () => {
		expect(reviewCommand("minitext", "m-1")).toBe("heimdall task review minitext/m-1");
	});
});

describe("quickstart commands and test suites (fix wave lane 2a)", () => {
	it("uses the double dash flag and keeps test suites out of the mined rows (X-14, M2-19)", async () => {
		const { QUICKSTART_STEPS, INSTALL_STEP, isTestSuite, minedRows: rows } = await import("./quickstart.utils");
		expect(QUICKSTART_STEPS.find((s) => s.key === "mine")?.command).toBe("heimdall tasks mine --limit 10");
		expect(INSTALL_STEP.key).toBe("install");
		expect(isTestSuite("zz-test-p10")).toBe(true);
		expect(isTestSuite("minitext")).toBe(false);
		expect(rows([{ id: "zz-test-x", tasks: [] } as never]).length).toBe(0);
	});
});
