import "@/lib/i18n";

import type { HeldOutSplitResult } from "@/lib/types/authoring";
import { describe, expect, it } from "vitest";
import { splitState } from "./reviewTab";

const result = { held_out: 4, public: 3, refused: [], next: "" } as unknown as HeldOutSplitResult;

describe("held-out split state (p11-5)", () => {
	it("drops the old result after an error and keeps Hold out disabled", () => {
		const s = splitState("25", result, { status: 400, data: { error: { message: "bad" } } });
		expect(s.result).toBeNull();
		expect(s.canApply).toBe(false);
	});

	it("blocks Preview for a share outside 1 to 90", () => {
		for (const raw of ["0", "100", "", "2.5"])
			expect(splitState(raw, null, undefined).problem).toBe("Hold out a whole number from 1 to 90 percent of the tasks.");
		expect(splitState("25", result, undefined)).toEqual({ problem: null, result, canApply: true });
	});
});