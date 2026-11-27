import { describe, expect, it } from "vitest";
import { importTabRedirect } from "./importTabRedirect";

describe("importTabRedirect (decision 5)", () => {
	it("sends ?tab=import to the catalog tab and keeps the query", () => {
		expect(importTabRedirect({ tab: "import", q: "swe" })).toEqual({ to: "/suites", search: { tab: "catalog", q: "swe" } });
	});
	it("leaves other tabs alone", () => {
		expect(importTabRedirect({ tab: "catalog" })).toBeNull();
		expect(importTabRedirect({})).toBeNull();
	});
});
