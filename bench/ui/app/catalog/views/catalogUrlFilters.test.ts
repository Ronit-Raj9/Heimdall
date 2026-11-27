import { describe, expect, it } from "vitest";
import { categoryFromUrl, filterToUrl, sourceFromUrl } from "./catalogUrlFilters";

describe("catalog URL filters", () => {
	it("reads known values and falls back to all", () => {
		expect(categoryFromUrl("memory")).toBe("memory");
		expect(categoryFromUrl("general")).toBe("general");
		expect(categoryFromUrl("nope")).toBe("all");
		expect(categoryFromUrl(null)).toBe("all");
		expect(sourceFromUrl("ready")).toBe("ready");
		expect(sourceFromUrl("x")).toBe("all");
	});
	it("drops the default from the address", () => {
		expect(filterToUrl("all")).toBeNull();
		expect(filterToUrl("needs")).toBe("needs");
	});
});
