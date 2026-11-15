import { describe, expect, it } from "vitest";
import { trendsRequest } from "./trendsApi";

describe("trendsRequest", () => {
	it("sends the bare route when no filter has a value", () => {
		expect(trendsRequest({})).toBe("/trends");
		expect(trendsRequest({ suite: "", agent: undefined })).toBe("/trends");
	});
	it("sends only the filters that have a value", () => {
		expect(trendsRequest({ suite: "coding-core@1.0.0", agent: "", metric: "tokens", synthetic: "include" })).toEqual({
			url: "/trends",
			params: { suite: "coding-core@1.0.0", metric: "tokens", synthetic: "include" },
		});
	});
});