import { keyMissingSearch } from "@/components/models/keyMissingNote";
import { describe, expect, it } from "vitest";
import { filterParam, hintParts, isNegative, returnParam } from "./modelsView";

describe("model keys helpers (lane 8a)", () => {
	it("accepts only same-site return paths", () => {
		expect(returnParam({ return: "/evaluate?step=5" })).toBe("/evaluate?step=5");
		expect(returnParam({ return: "https://evil.example" })).toBeNull();
		expect(returnParam({ return: "//evil.example" })).toBeNull();
		expect(returnParam({ return: "/\\evil.example" })).toBeNull();
		expect(returnParam({})).toBeNull();
	});

	it("reads the provider filter from the URL", () => {
		expect(filterParam({ filter: "open" })).toBe("open");
		expect(filterParam({ filter: 3 })).toBe("");
	});

	it("turns the console address of a provider hint into a link", () => {
		expect(hintParts("Create a key at console.anthropic.com/settings/keys")).toEqual([
			{ text: "Create a key at " },
			{ text: "console.anthropic.com/settings/keys", href: "https://console.anthropic.com/settings/keys" },
		]);
		expect(hintParts("Create one at https://platform.openai.com/api-keys.")).toEqual([
			{ text: "Create one at " },
			{ text: "https://platform.openai.com/api-keys", href: "https://platform.openai.com/api-keys" },
			{ text: "." },
		]);
		expect(hintParts("No key needed")).toEqual([{ text: "No key needed" }]);
	});

	it("flags negative amounts only", () => {
		expect(isNegative("-5")).toBe(true);
		expect(isNegative("0")).toBe(false);
		expect(isNegative("")).toBe(false);
		expect(isNegative("12.5")).toBe(false);
	});

	it("builds the Add API key link with a provider and a way back", () => {
		expect(keyMissingSearch("anthropic", "/evaluate?step=5")).toEqual({ tab: "keys", add: "anthropic", return: "/evaluate?step=5" });
		expect(keyMissingSearch("openai", "//x")).toEqual({ tab: "keys", add: "openai" });
	});
});