import "@/lib/i18n";

import { PairTable } from "@/components/runs/pairPicker";
import { render, tagOf } from "@/lib/testing/render";
import { frozenSuiteRef, type GridRow } from "@/lib/types/bench";
import { suiteVersionOf } from "@/components/shell/entityLink";
import { describe, expect, it, vi } from "vitest";
import { ArmProvenance } from "./compareView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

// 6-A-03, QC2-27-06: each run says what model evidence it has, independent of the server mode.
describe("ArmProvenance", () => {
	it("labels a scripted run and a run with recorded model calls", () => {
		expect(tagOf(render(<ArmProvenance arm="A" provenance="scripted" />), "runs-pair-provenance-A")).toContain('data-provenance="scripted"');
		expect(render(<ArmProvenance arm="A" provenance="scripted" />)).toContain("scripted");
		expect(render(<ArmProvenance arm="B" provenance="model" />)).toContain("real model");
	});
	it("never calls an unobserved or unreadable run model-played", () => {
		const unobserved = render(<ArmProvenance arm="B" provenance="unobserved" />);
		expect(unobserved).not.toContain("model-played");
		expect(unobserved).toContain("no model call recorded");
		const unavailable = render(<ArmProvenance arm="B" provenance="unavailable" note="proxy ledger invalid" />);
		expect(unavailable).toContain("provenance unavailable");
		expect(unavailable).toContain("proxy ledger invalid");
	});
	it("says nothing for an older server without the field", () => {
		expect(render(<ArmProvenance arm="A" provenance={undefined} />)).toBe("");
	});
});

// 6-A-05: the picker's task IDs open their task evidence in the suite.
describe("PairTable", () => {
	it("links each task into its suite", () => {
		const rows = [{ task: "t01", condition: "clean", discordant: false, cells: [] }] as unknown as GridRow[];
		const html = render(<PairTable rows={rows} arms={["A", "B"]} suite="coding-core@1.0.0" onPick={() => undefined} />);
		// QC2-27-09: the frozen version stays in the link, so the suite page can flag a newer revision.
		expect(tagOf(html, "pair-picker-task-t01")).toContain("/suites/coding-core%401.0.0");
	});
});

// QC2-27-09: task links carry the experiment's frozen suite version.
describe("frozenSuiteRef", () => {
	it("joins the frozen version, keeps an existing one, and needs a suite", () => {
		expect(frozenSuiteRef("example", "1.0.0")).toBe("example@1.0.0");
		expect(frozenSuiteRef("example@1.0.0", "2.0.0")).toBe("example@1.0.0");
		expect(frozenSuiteRef("example", undefined)).toBe("example");
		expect(frozenSuiteRef(undefined, "1.0.0")).toBeUndefined();
		expect(suiteVersionOf("example@1.0.0")).toBe("1.0.0");
		expect(suiteVersionOf("example")).toBeNull();
	});
});
