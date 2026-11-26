import "@/lib/i18n";

import type { SuiteTask } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// W4-44: the "Kind of work" column of the task table and its picker, which sends the PUT.
const sent = vi.hoisted(() => ({ calls: [] as unknown[], state: { isLoading: false, isError: false } }));
vi.mock("@/lib/store/apis/taskCategoriesApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/taskCategoriesApi")>()),
	useSetTaskCategoryMutation: () => [(arg: unknown) => sent.calls.push(arg), sent.state],
}));

const { SuiteTaskTable } = await import("./suiteTaskTable");
const { TaskCategoryCell, pickCategory } = await import("./taskCategoryCell");

const task = (id: string) =>
	({ id, title: id, checks: [], capabilities: [], hidden_test_files: 0, required_tests: 0 }) as unknown as SuiteTask;
const labels = {
	t1: { category: "bug_fix", source: "rule", sure: true, why: "r", at: "" },
	t2: { category: "refactor", source: "rule", sure: false, why: "r", at: "" },
};
const table = (canEdit: boolean, moreColumns = true) =>
	renderToStaticMarkup(
		<SuiteTaskTable
			suiteId="faq"
			rows={[task("t1"), task("t2"), task("t3")]}
			sort={{ key: "id", dir: "asc" }}
			onSort={() => undefined}
			selected={null}
			onOpen={() => undefined}
			moreColumns={moreColumns}
			categories={{ labels, canEdit }}
		/>,
	);

describe("Kind of work column", () => {
	it("shows the kind of work of each task with the expert columns", () => {
		const html = table(false);
		expect(html).toContain('data-testid="suites-head-category"');
		expect(html).toContain("Kind of work");
		expect(html).toMatch(/data-testid="suites-task-category-t1">Bug fix</);
		expect(html).toMatch(/data-testid="suites-task-category-t2">Refactor \(unsure\)</);
		expect(html).toMatch(/data-testid="suites-task-category-t3">Not labelled</);
	});

	it("stays out of the default columns", () => {
		expect(table(false, false)).not.toContain("suites-head-category");
	});

	it("is a picker for a member on the org's own benchmark, and a pick sends the PUT", () => {
		const html = table(true);
		expect(html).toMatch(/<select[^>]*aria-label="Change the kind of work of t1"[^>]*data-testid="suites-task-category-t1"/);
		expect(html).toMatch(/<option value="bug_fix" selected="">Bug fix<\/option>/);
		const save = (arg: unknown) => void sent.calls.push(arg);
		pickCategory(save, "faq", "t3", "docs");
		pickCategory(save, "faq", "t3", "");
		pickCategory(save, "faq", "t3", "not_a_kind");
		expect(sent.calls).toEqual([{ suiteId: "faq", task: "t3", category: "docs" }]);
	});

	it("says when the pick was not saved", () => {
		sent.state = { isLoading: false, isError: true };
		expect(renderToStaticMarkup(<TaskCategoryCell suiteId="faq" taskId="t1" label={labels.t1} canEdit />)).toContain(
			"The kind of work was not saved.",
		);
		sent.state = { isLoading: false, isError: false };
	});
});