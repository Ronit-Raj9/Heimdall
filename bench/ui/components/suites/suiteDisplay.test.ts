import { describe, expect, it } from "vitest";
import { orderSuites, plainDescription, suiteDisplayDescription, suiteDisplayName, suiteSections } from "./suiteDisplay";

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

describe("suite ordering", () => {
	const s = (id: string, builtin = false) => ({ id, ...(builtin ? { builtin: true, source: "library" as const } : {}) });
	const suites = [
		s("acme-web"),
		s("coding-core"),
		s("my-pack"),
		s("voicebench", true),
		s("hd-swe-verified", true),
		s("hd-harbor-tb2", true),
		s("another-own"),
	];
	it("puts the built-in library first, then org suites, then practice examples", () => {
		expect(orderSuites(suites).map((x) => x.id)).toEqual([
			"hd-harbor-tb2",
			"hd-swe-verified",
			"voicebench",
			"another-own",
			"my-pack",
			"coding-core",
			"acme-web",
		]);
	});
	it("treats an Acme id served by the library as built in", () => {
		const out = suiteSections([s("acme-web", true), s("acme-team")]);
		expect(out.builtin.map((x) => x.id)).toEqual(["acme-web"]);
		expect(out.practice.map((x) => x.id)).toEqual(["acme-team"]);
	});
	it("names every library benchmark", () => {
		expect(suiteDisplayName("hd-swe-verified")).toBe("SWE-bench Verified");
		expect(suiteDisplayDescription({ id: "voicebench", description: "" })).toBe("Spoken questions answered by a voice agent.");
		expect(suiteDisplayDescription({ id: "kernelbench", description: "" })).toContain("needs a GPU worker");
	});
});
