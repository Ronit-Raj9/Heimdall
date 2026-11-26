import { describe, expect, it } from "vitest";
import { closesOnKey } from "@/lib/hooks/useDialogFocus";

describe("closesOnKey (wave 4, p14-7)", () => {
	it("closes on an Escape no inner dialog handled", () => {
		expect(closesOnKey({ key: "Escape", defaultPrevented: false })).toBe(true);
	});
	it("stays open when an inner dialog handled the Escape, or for another key", () => {
		expect(closesOnKey({ key: "Escape", defaultPrevented: true })).toBe(false);
		expect(closesOnKey({ key: "Enter", defaultPrevented: false })).toBe(false);
	});
});