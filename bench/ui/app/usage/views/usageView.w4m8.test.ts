import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { rangeFromSearch, usageSearchState } from "./usageView";

const now = new Date("2026-11-23T12:00:00Z");

describe("reversed usage range", () => {
	it("says the dates were swapped and gives them sorted", () => {
		const r = rangeFromSearch({ from: "2026-11-20", to: "2026-11-10" }, now);
		expect(r.swapped).toBe(true);
		expect(r.from).toBe("2026-11-10");
		expect(r.to).toBe("2026-11-20");
		expect(rangeFromSearch({ from: "2026-11-10", to: "2026-11-20" }, now).swapped).toBeFalsy();
	});

	it("passes the swap on to the page state", () => {
		expect(usageSearchState({ from: "2026-11-20", to: "2026-11-10" }, now, false).swapped).toBe(true);
	});
});