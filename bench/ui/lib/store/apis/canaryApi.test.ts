import { describe, expect, it } from "vitest";
import { canaryPath, canaryRunRequest } from "./canaryApi";

describe("canary requests", () => {
	it("escapes the name as one path segment and posts a run", () => {
		expect(canaryPath("nightly")).toBe("/canaries/nightly");
		expect(canaryPath("../x?y")).toBe("/canaries/..%2Fx%3Fy");
		expect(canaryRunRequest("nightly")).toEqual({ url: "/canaries/nightly/run", method: "POST" });
	});
});