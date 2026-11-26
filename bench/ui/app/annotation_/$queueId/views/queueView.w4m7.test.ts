import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { decideShown, enterAction, skipNote } from "./queueView";

describe("enterAction (wave 4, p12-4)", () => {
	it("saves with the last choice, or hints before any choice", () => {
		expect(enterAction(null)).toBe("hint");
		expect(enterAction("correct")).toBe("save");
		expect(enterAction("incorrect")).toBe("save");
	});
});

describe("skipNote (wave 4, p12-3)", () => {
	it("notes when Next item gave the same item or none", () => {
		expect(skipNote("a", "a")).toBe(true);
		expect(skipNote("a", null)).toBe(true);
		expect(skipNote("a", "b")).toBe(false);
		expect(skipNote(null, "b")).toBe(false);
	});
});

describe("decideShown (wave 4, p12-7)", () => {
	it("offers Decide only on labelled rows that are not resolved", () => {
		expect(decideShown({ label_count: 0 })).toBe(false);
		expect(decideShown({ label_count: 1 })).toBe(true);
		expect(decideShown({ label_count: 2, resolution: { label: "correct", method: "consensus", at: "" } })).toBe(false);
	});
});