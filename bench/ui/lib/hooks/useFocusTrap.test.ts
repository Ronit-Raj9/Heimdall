import { describe, expect, it } from "vitest";
import { nextFocusIndex } from "@/lib/hooks/useFocusTrap";

describe("nextFocusIndex (M1-37 focus trap)", () => {
	it("wraps Tab on the last element to the first", () => {
		expect(nextFocusIndex(3, 2, false)).toBe(0);
	});
	it("wraps Shift+Tab on the first element to the last", () => {
		expect(nextFocusIndex(3, 0, true)).toBe(2);
	});
	it("lets the browser move focus in the middle", () => {
		expect(nextFocusIndex(3, 1, false)).toBeNull();
		expect(nextFocusIndex(3, 1, true)).toBeNull();
	});
	it("brings focus in from the panel or outside", () => {
		expect(nextFocusIndex(3, -1, false)).toBe(0);
		expect(nextFocusIndex(3, -1, true)).toBe(2);
	});
	it("does nothing without focusable elements", () => {
		expect(nextFocusIndex(0, -1, false)).toBeNull();
	});
	it("keeps a single element focused", () => {
		expect(nextFocusIndex(1, 0, false)).toBe(0);
		expect(nextFocusIndex(1, 0, true)).toBe(0);
	});
});
