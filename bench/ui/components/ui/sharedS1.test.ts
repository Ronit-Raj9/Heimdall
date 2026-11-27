import { scrollEdges } from "@/lib/hooks/useScrollEdges";
import { trapTarget } from "@/lib/hooks/useDialogFocus";
import { withDoneCount } from "@/lib/routing/pageTitle";
import { describe, expect, it } from "vitest";
import { menuKeyTarget } from "./dropdownMenu";
import { filterOptions, groupOptions, toggleAll, type OptionItem } from "./optionList";
import { sortRows } from "./useTableSort";

// Lane S1 shared pieces: pure helpers.
describe("menu keys", () => {
	it("moves, wraps and jumps", () => {
		expect(menuKeyTarget("ArrowDown", -1, 3)).toBe(0);
		expect(menuKeyTarget("ArrowDown", 2, 3)).toBe(0);
		expect(menuKeyTarget("ArrowUp", 0, 3)).toBe(2);
		expect(menuKeyTarget("Home", 2, 3)).toBe(0);
		expect(menuKeyTarget("End", 0, 3)).toBe(2);
		expect(menuKeyTarget("a", 0, 3)).toBeNull();
		expect(menuKeyTarget("ArrowDown", 0, 0)).toBeNull();
	});
});

describe("option list helpers", () => {
	const items: OptionItem[] = [
		{ id: "a", label: "Codex", meta: "gpt-6", group: "own" },
		{ id: "b", label: "Claude Code", meta: "m2", group: "builtin" },
		{ id: "c", label: "Plain", disabled: true, group: "builtin" },
	];
	it("filters by label and meta", () => {
		expect(filterOptions(items, "gpt").map((i) => i.id)).toEqual(["a"]);
		expect(filterOptions(items, " ").length).toBe(3);
	});
	it("groups in the given order", () => {
		const groups = groupOptions(items, [
			{ id: "builtin", label: "Built-in" },
			{ id: "own", label: "Yours" },
		]);
		expect(groups.map((g) => g.id)).toEqual(["builtin", "own"]);
	});
	it("select all skips disabled items and toggles off when all are on", () => {
		expect(toggleAll([], items)).toEqual(["a", "b"]);
		expect(toggleAll(["a", "b"], items)).toEqual([]);
	});
});

describe("scroll edges, focus trap, done count, numeric sort", () => {
	it("reports which edge hides content", () => {
		expect(scrollEdges({ scrollLeft: 0, scrollWidth: 100, clientWidth: 100 })).toEqual({ start: false, end: false });
		expect(scrollEdges({ scrollLeft: 0, scrollWidth: 300, clientWidth: 100 })).toEqual({ start: false, end: true });
		expect(scrollEdges({ scrollLeft: 200, scrollWidth: 300, clientWidth: 100 })).toEqual({ start: true, end: false });
	});
	it("wraps Tab at the ends of a dialog", () => {
		const items = ["x", "y", "z"];
		expect(trapTarget(items, "z", false)).toBe("x");
		expect(trapTarget(items, "x", true)).toBe("z");
		expect(trapTarget(items, "y", false)).toBeNull();
		expect(trapTarget(items, "outside", false)).toBe("x");
	});
	it("prefixes the title with finished work", () => {
		expect(withDoneCount("Runs · Heimdall", 0)).toBe("Runs · Heimdall");
		expect(withDoneCount("Runs · Heimdall", 2)).toBe("(2 done) Runs · Heimdall");
	});
	it("sorts task ids numerically", () => {
		const rows = [{ id: "t-10" }, { id: "t-2" }, { id: "t-1" }];
		expect(sortRows(rows, { key: "id", dir: "asc" }, { id: (r) => r.id }).map((r) => r.id)).toEqual(["t-1", "t-2", "t-10"]);
	});
});
