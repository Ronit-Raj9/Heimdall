import { describe, expect, it } from "vitest";
import { faultChange } from "./reportCard.utils";

describe("faultChange (N126)", () => {
	it("is the change from clean: negative hurts, positive helped, 0 is no change", () => {
		expect(faultChange({ drop: -0.67 })).toEqual({ text: "-67 pts", hurt: true, helped: false });
		expect(faultChange({ drop: 0.04 })).toEqual({ text: "+4 pts", hurt: false, helped: true });
		expect(faultChange({ drop: 0 })).toEqual({ text: "no change", hurt: false, helped: false });
		expect(faultChange(undefined)).toEqual({ text: "-", hurt: false, helped: false });
	});
});