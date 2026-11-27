import { describe, expect, it } from "vitest";
import { ADD_ENTRIES } from "./addChooser";
import { hasReadyTask, isImporterText, isPracticeSuite, plainDescription, suiteSections } from "./suiteDisplay";
import type { SuiteSummary } from "@/lib/types/bench";

describe("one add chooser (M6-41)", () => {
	it("has six entries, Tasks from your API among them", () => {
		expect(ADD_ENTRIES.map((e) => e.id)).toEqual(["file", "catalog", "api", "create", "code", "failures"]);
		expect(ADD_ENTRIES.find((e) => e.id === "api")?.to).toBe("/suites/from-api");
	});
});

describe("list rows (X-112, M6-46, M6-48)", () => {
	it("offers Run only when a task is ready", () => {
		expect(hasReadyTask({ admitted_count: 0 } as SuiteSummary)).toBe(false);
		expect(hasReadyTask({ admitted_count: 3 } as SuiteSummary)).toBe(true);
		expect(hasReadyTask({ admitted_count: 3, ready_known: true, ready_task_ids: [] } as unknown as SuiteSummary)).toBe(false);
	});

	it("puts zz-test fixtures with the practice examples", () => {
		expect(isPracticeSuite("zz-test-own")).toBe(true);
		const out = suiteSections([{ id: "zz-test-a" }, { id: "mine" }] as SuiteSummary[]);
		expect(out.practice.map((x) => x.id)).toEqual(["zz-test-a"]);
	});

	it("keeps the given order with keepOrder", () => {
		const out = suiteSections([{ id: "b" }, { id: "a" }] as SuiteSummary[], true);
		expect(out.org.map((x) => x.id)).toEqual(["b", "a"]);
	});

	it("never shows importer text as a description", () => {
		expect(isImporterText("Imported by bifrost-bench import harbor from x")).toBe(true);
		expect(isImporterText("bfcl@1.0 through the harbor importer (suite x).")).toBe(true);
		expect(plainDescription("Imported by bifrost-bench import harbor from a folder.")).toBe("");
		expect(plainDescription("Fix real bugs.")).toBe("Fix real bugs.");
	});
});
