import "@/lib/i18n";

import { PairTable } from "@/components/runs/pairPicker";
import { render, tagOf } from "@/lib/testing/render";
import type { GridRow } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { ArmProvenance } from "./compareView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

// 6-A-03: each arm says whether it is scripted, independent of the server mode.
describe("ArmProvenance", () => {
	it("labels a scripted arm and a model-played arm", () => {
		expect(tagOf(render(<ArmProvenance arm="A" synthetic />), "runs-pair-provenance-A")).toContain('data-synthetic="true"');
		expect(render(<ArmProvenance arm="A" synthetic />)).toContain("scripted");
		expect(render(<ArmProvenance arm="B" synthetic={false} />)).toContain("model-played");
	});
	it("says nothing for an older server without the field", () => {
		expect(render(<ArmProvenance arm="A" synthetic={undefined} />)).toBe("");
	});
});

// 6-A-05: the picker's task IDs open their task evidence in the suite.
describe("PairTable", () => {
	it("links each task into its suite", () => {
		const rows = [{ task: "t01", condition: "clean", discordant: false, cells: [] }] as unknown as GridRow[];
		const html = render(<PairTable rows={rows} arms={["A", "B"]} suite="coding-core@1.0.0" onPick={() => undefined} />);
		expect(tagOf(html, "pair-picker-task-t01")).toContain("/suites/coding-core");
	});
});
