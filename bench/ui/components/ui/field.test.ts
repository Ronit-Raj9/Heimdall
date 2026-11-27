import { describe, expect, it } from "vitest";
import { describedBy } from "@/components/ui/field";

describe("Field describedBy (M1-49)", () => {
	it("links the hint and the error lines", () => {
		expect(describedBy("f", true, true)).toBe("f-hint f-error");
		expect(describedBy("f", false, true)).toBe("f-error");
		expect(describedBy("f", false, false)).toBeUndefined();
	});
});
