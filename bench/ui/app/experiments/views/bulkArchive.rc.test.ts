import { describe, expect, it } from "vitest";
import { toggled } from "./bulkArchive";

// Round 2 lane RC (Q-169): the row boxes of the bulk archive.
describe("toggled", () => {
	it("adds and removes one id", () => {
		const one = toggled(new Set(), "e1");
		expect([...one]).toEqual(["e1"]);
		expect([...toggled(one, "e1")]).toEqual([]);
	});
});