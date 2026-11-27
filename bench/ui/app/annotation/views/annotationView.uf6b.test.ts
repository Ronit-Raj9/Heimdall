import { describe, expect, it } from "vitest";
import { countText, whoLabels } from "./annotationView";

describe("labelling list and form (X-47, M6-72)", () => {
	it("shows a dash for zero", () => {
		expect(countText(0)).toBe("-");
		expect(countText(3)).toBe("3");
	});

	it("maps the one Who labels choice to the queue settings", () => {
		expect(whoLabels("any")).toEqual({ assignment: "open", named: false });
		expect(whoLabels("only")).toEqual({ assignment: "open", named: true });
		expect(whoLabels("assigned")).toEqual({ assignment: "fixed", named: true });
	});
});
