import "@/lib/i18n";

import { mutation, render } from "@/lib/testing/render";
import type { InboxItem } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { BUILD_TASK_BULK_MAX, BuildTaskBulk, bulkTotal } from "./buildTaskBulk";

vi.mock("./buildTaskApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("./buildTaskApi")>()),
	useStartBuildTaskMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/modelsApi", () => ({ useGetModelsQuery: () => ({ data: undefined }) }));
vi.mock("@/components/models/groupedModelPicker", () => ({ GroupedModelPicker: () => null }));

const item = (id: string, state = "new") => ({ id, state, title: `failure ${id}` }) as unknown as InboxItem;

describe("Build tasks from these failures (round 7 lane T3)", () => {
	it("totals the caps and caps the selection at 10", () => {
		expect(bulkTotal(3, 0.5)).toBe(1.5);
		expect(BUILD_TASK_BULK_MAX).toBe(10);
	});
	it("lists open failures with a total and the paid button", () => {
		const html = render(<BuildTaskBulk items={[item("inc-1"), item("inc-2", "accepted")]} suites={[{ id: "support-evals", template: "qa" }]} />);
		expect(html).toContain('data-testid="build-task-bulk-inc-1"');
		expect(html).not.toContain("build-task-bulk-inc-2");
		expect(html).toContain('data-testid="build-task-bulk-start"');
		expect(html).toContain("Total up to $0");
	});
});
