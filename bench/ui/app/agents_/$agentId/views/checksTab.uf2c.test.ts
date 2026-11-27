import { describe, expect, it } from "vitest";
import { selectIds } from "./checksTab";

describe("checks tab selects (M2-68)", () => {
	it("sorts ids and keeps the current value listed", () => {
		expect(selectIds(["b", "a"], "")).toEqual(["a", "b"]);
		expect(selectIds(["b", "a"], "coding-core")).toEqual(["a", "b", "coding-core"]);
		expect(selectIds(["a"], "a")).toEqual(["a"]);
	});
});
