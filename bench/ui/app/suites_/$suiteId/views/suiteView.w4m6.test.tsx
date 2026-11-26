import { resolveTab } from "@/components/shell/routeTabs";
import { describe, expect, it } from "vitest";
import { describedByOwnWords, editTabEntry, headerLine } from "./suiteView";

describe("benchmark page header (p10-7)", () => {
	it("shows only the version when the title is the id", () => {
		expect(headerLine("faq-bench", "faq-bench", "0.1.0")).toBe("@0.1.0");
		expect(headerLine("Terminal-Bench 2", "hd-harbor-tb2", "2.0")).toBe("hd-harbor-tb2@2.0");
		expect(headerLine("faq-bench", "faq-bench", "")).toBe("");
	});

	it("describes voice and support benchmarks in their own words", () => {
		expect(describedByOwnWords({ track: "support" })).toBe(true);
		expect(describedByOwnWords({ track: "voice" as never })).toBe(true);
		expect(describedByOwnWords({ track: "qa" })).toBe(false);
	});
});

describe("built-in benchmark edit tab (p12-8)", () => {
	it("opens the hidden edit tab from ?tab=edit so the read-only note shows", () => {
		const tabs = [{ id: "tasks", label: "Tasks" }, editTabEntry("Edit files", true)];
		expect(resolveTab(tabs, "edit", "tasks")).toBe("edit");
		expect(resolveTab(tabs, "files", "tasks")).toBe("edit");
	});
});