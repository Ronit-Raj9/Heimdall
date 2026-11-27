import { describe, expect, it } from "vitest";
import { addCheck, EMPTY_FORM, hostedMcpAddress, mcpFormDirty, validUrl, type McpCheck } from "./mcpTestView";

describe("MCP test page (lane 5b)", () => {
	it("accepts only full http and https addresses (M5-50)", () => {
		expect(validUrl("https://mcp.example.com/mcp")).toBe(true);
		expect(validUrl(" http://localhost:8080/mcp ")).toBe(true);
		expect(validUrl("mcp.example.com")).toBe(false);
		expect(validUrl("ftp://x.example")).toBe(false);
		expect(validUrl("")).toBe(false);
	});
	it("prints this site's /mcp address (X-36)", () => {
		expect(hostedMcpAddress("https://heimdall.example")).toBe("https://heimdall.example/mcp");
		expect(hostedMcpAddress("https://heimdall.example/")).toBe("https://heimdall.example/mcp");
		expect(hostedMcpAddress(undefined)).toBe("");
	});
	it("keeps the last checks per endpoint, newest first (M5-49)", () => {
		let h: McpCheck[] = [];
		for (let i = 0; i < 4; i++) h = addCheck(h, { url: "a", at: String(i), reached: true, overall: i }, 3);
		h = addCheck(h, { url: "b", at: "9", reached: false }, 3);
		expect(h.map((c) => `${c.url}${c.at}`)).toEqual(["b9", "a3", "a2", "a1"]);
	});
});

describe("MCP test form draft (X-82)", () => {
	it("is dirty once an address, secret or header is typed", () => {
		expect(mcpFormDirty(EMPTY_FORM)).toBe(false);
		expect(mcpFormDirty({ ...EMPTY_FORM, url: "https://x" })).toBe(true);
		expect(mcpFormDirty({ ...EMPTY_FORM, header: "X-Key" })).toBe(true);
	});
});
