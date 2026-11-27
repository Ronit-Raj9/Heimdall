import { describe, expect, it } from "vitest";
import { builderSearch } from "@/components/onboard/inventoryPanel";
import { leaveGuarded } from "@/app/suites_/add/views/addBenchmarkView";
import { specNameFromUrl } from "./fromApiView";

describe("OpenAPI spec carried from the wizard (Q-36, Q-269)", () => {
	it("the builder link carries the spec address for an OpenAPI link only", () => {
		expect(builderSearch("openapi", " https://example.com/api/shop.yaml ")).toEqual({ spec: "https://example.com/api/shop.yaml" });
		expect(builderSearch("openapi", "shop.yaml")).toBeUndefined();
		expect(builderSearch("github", "https://github.com/a/b")).toBeUndefined();
	});
	it("names the spec after the last part of its address", () => {
		expect(specNameFromUrl("https://example.com/api/shop.yaml?v=1")).toBe("shop.yaml");
		expect(specNameFromUrl("not a url")).toBe("");
	});
	it("Look inside alone never asks before leaving", () => {
		expect(leaveGuarded({ edited: false, drafted: false })).toBe(false);
		expect(leaveGuarded({ edited: true, drafted: false })).toBe(true);
		expect(leaveGuarded({ edited: true, drafted: true })).toBe(false);
	});
});
