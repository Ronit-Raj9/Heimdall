import { describe, expect, it } from "vitest";
import { clipText, maskKeys } from "./stepTimeline.utils";

// Invented key-shaped strings, not real keys.
describe("masking key-shaped strings on the run page", () => {
	it("masks the per-run virtual key, even only its start", () => {
		const out = clipText(`curl -H "Authorization: Bearer sk-bf-abcd1234efgh" http://gw`);
		expect(out).not.toContain("abcd1234efgh");
		expect(out).toContain("sk-bf-[masked]");
		expect(maskKeys("KEY=sk-bf-zz99")).toBe("KEY=sk-bf-[masked]");
	});
	it("masks other keys and bearer tokens", () => {
		expect(maskKeys("OPENAI_API_KEY=sk-testFAKEFAKEFAKE0000")).not.toContain("FAKEFAKE");
		expect(maskKeys("Authorization: Bearer abcDEF123456789")).toBe("Authorization: Bearer [masked]");
	});
	it("leaves plain text alone", () => {
		expect(clipText("ls -la /workspace")).toBe("ls -la /workspace");
	});
});
