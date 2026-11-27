import { describe, expect, it } from "vitest";
import { urlState } from "./mcpTestView";

describe("MCP address check (Q-290)", () => {
	it("a typed address that is not a URL holds the button and says so", () => {
		expect(urlState("not a url", false)).toEqual({ bad: true, blocks: true });
		expect(urlState("https://", false)).toEqual({ bad: true, blocks: true });
		expect(urlState("https://mcp.example.com/mcp", false)).toEqual({ bad: false, blocks: false });
	});
	it("an empty address is said only after a press and never holds the button", () => {
		expect(urlState("", false)).toEqual({ bad: false, blocks: false });
		expect(urlState("", true)).toEqual({ bad: true, blocks: false });
	});
});
