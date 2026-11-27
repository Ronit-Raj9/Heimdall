import { describe, expect, it } from "vitest";
import { isTestObject, withoutTests } from "./testObjects";

// M1-18: test objects are hidden by default.
describe("test objects", () => {
	it("knows test names and stand-in descriptions", () => {
		expect(isTestObject("zz-test-loop")).toBe(true);
		expect(isTestObject("Zz test comparison")).toBe(true);
		expect(isTestObject("codex")).toBe(false);
		expect(isTestObject("demo", { description: "A stub agent for the example benchmarks" })).toBe(true);
		expect(isTestObject(null)).toBe(false);
	});
	it("splits a list and counts what it hid", () => {
		const items = ["zz-test-a", "codex", "zz-test-b"];
		expect(withoutTests(items, (i) => isTestObject(i), false)).toEqual({ shown: ["codex"], hidden: 2 });
		expect(withoutTests(items, (i) => isTestObject(i), true)).toEqual({ shown: items, hidden: 2 });
	});
});
