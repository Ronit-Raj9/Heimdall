import "@/lib/i18n";

import i18n from "i18next";
import { describe, expect, it } from "vitest";
import { saveErrorText, suitePathProblem, uploadTargetText } from "./editTab";

const t = i18n.getFixedT("en", "w4m6") as never;

describe("new file path check (p11-1)", () => {
	it("refuses paths the server cannot store", () => {
		for (const bad of ["/abs.txt", "../x.txt", "a/../b.txt", "a/./b.txt", "a\\b.txt", "a b/c:d*?.txt"])
			expect(suitePathProblem(bad)).not.toBeNull();
		expect(suitePathProblem("tasks/t01/notes-1.md")).toBeNull();
	});

	it("says Could not save with the reason", () => {
		expect(saveErrorText(t, { status: 422, data: { error: { message: "line 3: did not find expected ',' or ']'" } } })).toBe(
			"Could not save: line 3: did not find expected ',' or ']'",
		);
		expect(saveErrorText(t, { status: 422, data: { error: { message: "Could not save a.yaml: line 1: bad" } } })).toBe(
			"Could not save a.yaml: line 1: bad",
		);
	});
});

describe("upload target (p11-4)", () => {
	it("names the folder files go into", () => {
		expect(uploadTargetText(t, "")).toBe("Files go into the top folder of the benchmark.");
		expect(uploadTargetText(t, "tasks/t01")).toBe("Files go into tasks/t01.");
	});
});