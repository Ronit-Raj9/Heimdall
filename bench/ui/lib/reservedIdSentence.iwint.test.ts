import i18n from "@/lib/i18n";
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// Lead's rule 16: one sentence for a reserved benchmark id everywhere: the server's 409 for a new
// benchmark, a draft and a publish (handlers/suites.go sendReservedSuiteID), and the forms' own checks
// on New benchmark and Add benchmark, in the words of D1 N46.
const sentence = "coding-core is the name of a built-in benchmark. Pick another id.";

describe("reserved benchmark id sentence", () => {
	it("is the same on both forms and on the server", () => {
		expect(i18n.t("newSuite.builtin", { ns: "hw02", id: "coding-core" })).toBe(sentence);
		expect(i18n.t("plan.suiteBuiltin", { ns: "onboard", id: "coding-core" })).toBe(sentence);
		const server = readFileSync(new URL("../../handlers/suites.go", import.meta.url), "utf8");
		expect(server).toContain('id+" is the name of a built-in benchmark. Pick another id."');
		const authoring = readFileSync(new URL("../../handlers/suiteauthoring.go", import.meta.url), "utf8");
		expect(authoring).not.toContain("is a built-in benchmark. Pick another id.");
	});
});