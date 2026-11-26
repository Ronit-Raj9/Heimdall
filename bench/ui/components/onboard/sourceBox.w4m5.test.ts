import { describe, expect, it } from "vitest";
import { sourceHintShown } from "./sourceBox";

describe("sourceHintShown (p10-9c)", () => {
	it("shows the hint only while nothing is given", () => {
		expect(sourceHintShown({ link: "", rows: "", file: null })).toBe(true);
		expect(sourceHintShown({ link: "https://huggingface.co/datasets/x/y", rows: "", file: null })).toBe(false);
	});
});