import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { claudeCodeCommand, cursorInstallLink, McpInstallPanel, vscodeConfig } from "./mcpInstall";

describe("McpInstallPanel", () => {
	it("links Cursor with the server config and no secret", () => {
		const link = cursorInstallLink("https://heimdall.example");
		expect(link.startsWith("cursor://anysphere.cursor-deeplink/mcp/install?name=heimdall&config=")).toBe(true);
		const config = JSON.parse(atob(decodeURIComponent(link.split("config=")[1] ?? "")));
		expect(config).toEqual({ command: "heimdall", args: ["mcp", "serve"], env: { HEIMDALL_URL: "https://heimdall.example" } });
		expect(link).not.toMatch(/token|hm_/i);
	});
	it("gives the Claude Code command and the VS Code file", () => {
		expect(claudeCodeCommand("https://h.example")).toBe("claude mcp add heimdall -e HEIMDALL_URL=https://h.example -- heimdall mcp serve");
		expect(JSON.parse(vscodeConfig("https://h.example")).servers.heimdall.type).toBe("stdio");
	});
	it("renders both buttons and the login line for this server", () => {
		const html = render(<McpInstallPanel origin="https://heimdall.example/" />);
		expect(html).toContain('data-testid="mcp-add-cursor"');
		expect(html).toContain('data-testid="mcp-add-claude-code"');
		expect(html).toContain("heimdall login -url https://heimdall.example");
		expect(html).toContain("Add to Cursor");
		expect(html).toContain("Add to Claude Code");
	});
});
