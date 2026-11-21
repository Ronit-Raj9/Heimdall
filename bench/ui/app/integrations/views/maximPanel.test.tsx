import "@/lib/i18n";

import type { MaximConnection } from "@/lib/types/traceTools";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MaximForm, canSaveMaxim, maximDraft, maximUpdateBody } from "./maximPanel";

const conn: MaximConnection = {
	connected: true,
	workspace_id: "ws1",
	repo_id: "repo1",
	auto_push: true,
	link_template: "",
	key_last4: "9xQz",
	pushed: 12,
	failed: 1,
	last_error: "HTTP 401",
};

describe("Maxim panel", () => {
	it("shows the stored key, counts and last error", () => {
		const html = renderToStaticMarkup(
			<MaximForm conn={conn} draft={maximDraft(conn)} onDraft={() => {}} onSave={() => {}} onTest={() => {}} onDisconnect={() => {}} />,
		);
		expect(html).toContain("Stored key ending in 9xQz");
		expect(html).toContain("Sent 12");
		expect(html).toContain("Failed 1");
		expect(html).toContain("Last error: HTTP 401");
		expect(html).toContain('type="password"');
		expect(html).toContain("maxim-disconnect");
		expect(html).toContain("app.getmaxim.ai/workspace/{workspace_id}");
	});
	it("hides disconnect when not connected", () => {
		const html = renderToStaticMarkup(
			<MaximForm draft={maximDraft(undefined)} onDraft={() => {}} onSave={() => {}} onTest={() => {}} onDisconnect={() => {}} />,
		);
		expect(html).toContain("Not connected");
		expect(html).not.toContain("maxim-disconnect");
	});
	it("keeps the stored key when the key field is empty", () => {
		const d = { ...maximDraft(conn), workspaceId: " ws2 " };
		expect(maximUpdateBody(d)).toEqual({ workspace_id: "ws2", repo_id: "repo1", auto_push: true });
		expect(maximUpdateBody({ ...d, apiKey: "k", linkTemplate: "https://x/{trace_id}" })).toMatchObject({ api_key: "k", link_template: "https://x/{trace_id}" });
		expect(canSaveMaxim(d, conn)).toBe(true);
		expect(canSaveMaxim(maximDraft(undefined), undefined)).toBe(false);
		expect(canSaveMaxim({ ...maximDraft(undefined), workspaceId: "w", repoId: "r", apiKey: "k" }, undefined)).toBe(true);
	});
});
