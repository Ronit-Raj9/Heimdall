import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { fixedProblem } from "./annotationView";

describe("fixedProblem (wave 4, p12-7)", () => {
	it("asks for enough named people before a fixed queue can be created", () => {
		expect(fixedProblem("fixed", 1, 2)).toBe(
			"Name at least 2 people under Only these people (one for each label an item wants), or lower People per item.",
		);
		expect(fixedProblem("fixed", 2, 2)).toBeNull();
		expect(fixedProblem("open", 0, 3)).toBeNull();
	});
});