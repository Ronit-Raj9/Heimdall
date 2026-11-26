import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

// N288, X4: the benchmarks list row Delete. The mutation state is set per test.
const mutation = vi.hoisted(() => ({ state: { isLoading: false, error: undefined as unknown } }));
vi.mock("@/lib/store/apis/suiteAuthoringApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/suiteAuthoringApi")>()),
	useDeleteSuiteMutation: () => [() => ({ unwrap: () => Promise.resolve() }), mutation.state],
}));

const { SuiteRowDelete, deleteSuiteRow, suiteRowDeletable } = await import("./suiteRowDelete");

const own = { id: "faq-bench", builtin: false, source: "project" };
const library = { id: "hd-swe-verified", builtin: true, source: "library" };

describe("benchmark row Delete (N288)", () => {
	it("is hidden for members and viewers and on a built-in benchmark", () => {
		expect(suiteRowDeletable(hostedSession("admin"), own)).toBe(true);
		expect(suiteRowDeletable(hostedSession("member"), own)).toBe(false);
		expect(suiteRowDeletable(hostedSession("viewer"), own)).toBe(false);
		expect(suiteRowDeletable(hostedSession("owner"), library)).toBe(false);
		expect(suiteRowDeletable(hostedSession("owner"), { id: "x", source: "library" })).toBe(false);
		expect(suiteRowDeletable(hostedSession("owner"), { id: "coding-core", builtin: false, source: "project" })).toBe(false);
		mutation.state = { isLoading: false, error: undefined };
		expect(render(<SuiteRowDelete suite={own} name="FAQ" />, hostedSession("member"))).toBe("");
		expect(render(<SuiteRowDelete suite={library} name="SWE" />, hostedSession("owner"))).toBe("");
		expect(render(<SuiteRowDelete suite={own} name="FAQ" />, hostedSession("admin"))).toContain(
			'data-testid="suites-row-faq-bench-delete"',
		);
	});

	it("asks the confirm question before deleting, and deletes nothing when cancelled", async () => {
		const asked: { message: string; opts?: { destructive?: boolean } }[] = [];
		const removed: string[] = [];
		const remove = async (id: string) => void removed.push(id);
		const no = async (message: string, opts?: { destructive?: boolean }) => (asked.push({ message, opts }), false);
		const question = "Delete the benchmark FAQ? Its tasks and files are removed. Past results stay.";
		expect(await deleteSuiteRow(no, remove, "faq-bench", question, { confirm: "Delete", cancel: "Cancel" })).toBe(false);
		expect(asked[0].message).toBe(question);
		expect(asked[0].opts?.destructive).toBe(true);
		expect(removed).toEqual([]);
		expect(await deleteSuiteRow(async () => true, remove, "faq-bench", question, { confirm: "Delete", cancel: "Cancel" })).toBe(true);
		expect(removed).toEqual(["faq-bench"]);
	});

	it("shows the server's refusal sentence", () => {
		const sentence = "faq-bench is used by the schedule nightly. Remove the schedule first.";
		mutation.state = { isLoading: false, error: { status: 409, data: { error: { message: sentence } } } };
		const html = render(<SuiteRowDelete suite={own} name="FAQ" />, hostedSession("admin"));
		expect(html).toContain('data-testid="suites-row-faq-bench-delete-error"');
		expect(html).toContain(sentence);
		mutation.state = { isLoading: false, error: undefined };
	});
});