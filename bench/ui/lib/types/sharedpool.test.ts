import { describe, expect, it } from "vitest";
import { isPinnedImage } from "./sharedpool";

const digest = `sha256:${"a".repeat(64)}`;

describe("isPinnedImage", () => {
	it("accepts digest-pinned refs", () => {
		expect(isPinnedImage(`ghcr.io/acme/agent@${digest}`)).toBe(true);
		expect(isPinnedImage(`python:3.12@${digest}`)).toBe(true);
	});
	it("refuses tags and bad digests", () => {
		expect(isPinnedImage("python:3.12")).toBe(false);
		expect(isPinnedImage("python@sha256:abc")).toBe(false);
		expect(isPinnedImage(`Python@${digest}`)).toBe(false);
	});
});
