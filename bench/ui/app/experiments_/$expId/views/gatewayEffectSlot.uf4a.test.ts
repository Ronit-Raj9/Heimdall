import { describe, expect, it } from "vitest";
import { effectOf } from "./gatewayEffectSlot";

// M4-7: the optional gateway effect answer; null renders nothing.
describe("effectOf", () => {
	it("reads {effect: null} and empty answers as no effect", () => {
		expect(effectOf({ mode: "recorded", effect: null })).toBeNull();
		expect(effectOf(undefined)).toBeNull();
		expect(effectOf(null)).toBeNull();
		expect(effectOf({})).toBeNull();
	});
	it("passes a feature test's effect through", () => {
		const e = { feature: "cache", exp_id: "e1", control: "A", state: "measured", headline: "x", tasks: 3 };
		expect(effectOf(e)).toBe(e);
	});
});