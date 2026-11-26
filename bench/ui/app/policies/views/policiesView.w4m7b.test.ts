import "@/lib/i18n";

import { starterPolicy } from "@/components/govern/policyForm";
import { describe, expect, it } from "vitest";
import { fileNameNote, freePolicyFile } from "./policiesView";

describe("rule set file name", () => {
	it("says what a changed file name will be saved as", () => {
		expect(fileNameNote("Bad Name")).toBe("It will be saved as bad-name.yaml.");
		expect(fileNameNote("good.yaml")).toBeNull();
		expect(fileNameNote("!!")).toBeNull();
	});

	it("the new set's name follows the free file name", () => {
		const file = freePolicyFile(["my-release-policy.yaml"]);
		const p = starterPolicy(file.replace(/\.ya?ml$/, ""));
		expect(p.name).toBe("my-release-policy-2");
		expect(p.description).toBe("");
		expect(p.checks.length).toBe(3);
	});
});