import { describe, expect, it } from "vitest";
import { killAsk } from "./adminView";

describe("emergency stop confirm (M8-57)", () => {
	it("asks in both directions", () => {
		expect(killAsk(false)).toMatchObject({ question: "kill.confirmOn", destructive: true });
		expect(killAsk(true)).toMatchObject({ question: "kill.confirmOff", label: "kill.allow" });
	});
});