import { describe, expect, it } from "vitest";
import { branchItems } from "./githubTab";

describe("base branch choices", () => {
	it("keeps the default branch when the loaded refs do not include it", () => {
		const items = branchItems("main", [{ name: "dev" }, { name: "feature" }]);
		expect(items).toContain("main");
		expect(items).toEqual(["main", "dev", "feature"]);
	});

	it("lists each branch once and drops an empty base", () => {
		expect(branchItems("dev", [{ name: "dev" }, { name: "main" }])).toEqual(["dev", "main"]);
		expect(branchItems("", [{ name: "main" }])).toEqual(["main"]);
	});
});