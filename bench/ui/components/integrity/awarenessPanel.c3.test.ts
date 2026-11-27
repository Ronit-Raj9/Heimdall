import { describe, expect, it } from "vitest";
import { signalsColumnNeeded } from "./awarenessPanel";

describe("signalsColumnNeeded (M4-43)", () => {
	it("hides the column when every arm has one signal per flagged run", () => {
		expect(signalsColumnNeeded([{ runs_with_signals: 2, signals: 2 }, { runs_with_signals: 0, signals: 0 }])).toBe(false);
	});
	it("shows it when an arm has more signals than runs", () => {
		expect(signalsColumnNeeded([{ runs_with_signals: 1, signals: 3 }])).toBe(true);
	});
});
