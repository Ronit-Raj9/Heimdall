import { describe, expect, it } from "vitest";
import type { CatalogProvider } from "@/components/models/groupedModelPicker";
import { unknownModel } from "./ownTasks";

const catalog = [{ id: "openai", name: "OpenAI", models: [{ id: "gpt-5-mini", purposes: [], rank: 1 }] }] as unknown as CatalogProvider[];

describe("unknownModel (w4m2)", () => {
	it("flags a judge model the catalog does not list", () => {
		expect(unknownModel(catalog, "openai", "zz-test-p03-nonexistent-judge")).toBe(true);
		expect(unknownModel(catalog, "openai", "gpt-5-mini")).toBe(false);
	});
	it("says nothing before the catalog has loaded or while the model is empty", () => {
		expect(unknownModel(undefined, "openai", "zz-test-p03-nonexistent-judge")).toBe(false);
		expect(unknownModel(catalog, "openai", "")).toBe(false);
	});
});