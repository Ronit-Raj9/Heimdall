import { describe, expect, it } from "vitest";
import { environmentLabelKey } from "./suiteFacts";

describe("environmentLabelKey", () => {
	it("maps known environment ids to plain words keys", () => {
		expect(environmentLabelKey("docker_workspace")).toBe("workspace");
		expect(environmentLabelKey("docker")).toBe("workspace");
		expect(environmentLabelKey("node")).toBe("node");
	});
	it("returns null for unknown or empty ids", () => {
		expect(environmentLabelKey("gpu-cluster")).toBeNull();
		expect(environmentLabelKey(undefined)).toBeNull();
	});
});
