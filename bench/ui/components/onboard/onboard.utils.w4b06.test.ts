import { describe, expect, it } from "vitest";

import { suiteIdCheck } from "./onboard.utils";

describe("suiteIdCheck reserved ids", () => {
	it("a reserved id is built in", () => {
		expect(suiteIdCheck("coding-core", [], ["coding-core"])).toEqual({ state: "builtin" });
	});
	it("an id that is not reserved stays new", () => {
		expect(suiteIdCheck("my-suite", [], ["coding-core"])).toEqual({ state: "new" });
		expect(suiteIdCheck("coding-core", [])).toEqual({ state: "new" });
	});
});