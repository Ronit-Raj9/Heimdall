import { describe, expect, it } from "vitest";
import { formatDuration } from "./utils";

describe("formatDuration (M3-24)", () => {
	it("uses seconds, minutes and hours in one format", () => {
		expect(formatDuration(13)).toBe("13 s");
		expect(formatDuration(252)).toBe("4 min 12 s");
		expect(formatDuration(240)).toBe("4 min");
		expect(formatDuration(3900)).toBe("1 h 5 min");
		expect(formatDuration(undefined)).toBe("-");
	});
});
