import { describe, expect, it } from "vitest";
import { controlledSelectValue } from "./utils";

// 6-B-04: board and trends pickers are controlled before their response arrives.
describe("controlledSelectValue", () => {
	it("never hands Radix undefined", () => {
		expect(controlledSelectValue(undefined)).toBe("");
		expect(controlledSelectValue(null)).toBe("");
		expect(controlledSelectValue("coding-core")).toBe("coding-core");
	});
});
