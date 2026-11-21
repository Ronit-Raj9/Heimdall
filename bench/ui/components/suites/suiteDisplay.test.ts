import { describe, expect, it } from "vitest";
import { plainDescription, suiteDisplayDescription, suiteDisplayName } from "./suiteDisplay";

describe("suiteDisplay", () => {
	it("uses curated names and descriptions for known ids", () => {
		expect(suiteDisplayName("hd-harbor-tb2")).toBe("Terminal-Bench 2");
		expect(suiteDisplayName("acme-team")).toBe("Team of agents");
		expect(suiteDisplayDescription({ id: "coding-core", description: "Small Python repositories" })).toBe(
			"Fix small Python projects so their hidden tests pass.",
		);
	});

	it("title-cases unknown ids", () => {
		expect(suiteDisplayName("my_new-pack")).toBe("My New Pack");
	});

	it("keeps only the first sentence with jargon stripped, cut near 90 characters", () => {
		expect(plainDescription("Shell tasks imported in the Harbor shape: tests/test.sh writes a reward file. More.")).toBe(
			"Shell tasks imported in the Harbor shape: writes a reward file.",
		);
		expect(plainDescription("skillsbench@1.0 through the harbor importer (suite hd-harbor-skills)")).toBe("Through the harbor importer");
		const long = plainDescription(`${"word ".repeat(40)}end.`);
		expect(long.length).toBeLessThanOrEqual(93);
		expect(long.endsWith("...")).toBe(true);
		expect(plainDescription(undefined)).toBe("");
	});
});
