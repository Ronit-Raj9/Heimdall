import { describe, expect, it } from "vitest";
import { splitRevoked } from "./revokedFilter";

describe("splitRevoked", () => {
	const items = [
		{ id: "a", live: true },
		{ id: "b", live: false },
		{ id: "c", live: false },
	];
	it("shows live items and counts the rest", () => {
		const r = splitRevoked(items, (i) => i.live, false);
		expect(r.shown.map((i) => i.id)).toEqual(["a"]);
		expect(r.hidden).toBe(2);
	});
	it("shows every item when asked", () => {
		expect(splitRevoked(items, (i) => i.live, true).shown).toHaveLength(3);
	});
});