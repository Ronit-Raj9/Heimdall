import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

const steps = [{ seq: 1, kind: "model_call", turn: 1 }];
vi.mock("@/lib/store/apis/runStepsApi", () => ({
	// A skipped query keeps its old data; only currentData follows the argument.
	useGetRunStepsQuery: () => ({ data: { steps }, currentData: undefined, error: undefined }),
}));

const { TraceDiff } = await import("./traceDiff");

describe("TraceDiff with an empty run id", () => {
	it("drops the old table and asks for a run id", () => {
		const html = render(<TraceDiff runId="r_1" otherRunId="" />);
		expect(html).not.toContain("trace-diff-first");
		expect(html).toContain("Enter a run id to compare with this run.");
	});
});