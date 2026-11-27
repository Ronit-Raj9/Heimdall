import { describe, expect, it } from "vitest";
import { menuPosition } from "./dropdownMenu";
import { foldEmpty } from "./facts";
import { columnHasData } from "./table";

describe("menuPosition", () => {
	const button = { top: 100, bottom: 132, left: 900, right: 932 };
	it("sits below the button, right edges aligned", () => {
		expect(menuPosition(button, { width: 192, height: 80 }, 1440, 900)).toEqual({ top: 136, left: 740, placement: "below" });
	});
	it("flips above near the bottom of the window", () => {
		const low = { top: 840, bottom: 872, left: 900, right: 932 };
		expect(menuPosition(low, { width: 192, height: 80 }, 1440, 900)).toEqual({ top: 756, left: 740, placement: "above" });
	});
	it("stays 8 px inside a phone window", () => {
		const edge = { top: 100, bottom: 132, left: 10, right: 42 };
		expect(menuPosition(edge, { width: 192, height: 80 }, 390, 844).left).toBe(8);
		expect(menuPosition(edge, { width: 192, height: 80 }, 390, 844, "start").left).toBe(10);
		const right = { top: 100, bottom: 132, left: 370, right: 402 };
		expect(menuPosition(right, { width: 192, height: 80 }, 390, 844, "start").left).toBe(390 - 8 - 192);
	});
});

describe("columnHasData and foldEmpty", () => {
	it("treats 0 and false as data, empty as none", () => {
		expect(columnHasData([{ v: null }, { v: "" }, { v: [] }], (r) => r.v)).toBe(false);
		expect(columnHasData([{ v: null }, { v: 0 }], (r) => r.v)).toBe(true);
	});
	it("splits rows to show and fold", () => {
		expect(foldEmpty(["a", "unknown", "b", "unknown"], (x) => x === "unknown")).toEqual({ shown: ["a", "b"], folded: ["unknown", "unknown"] });
	});
});
