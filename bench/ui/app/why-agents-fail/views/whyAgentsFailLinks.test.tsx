import "@/lib/i18n";

import { MaximForm, maximDraft } from "@/app/integrations/views/maximPanel";
import type { MaximConnection } from "@/lib/types/traceTools";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { MaximFailuresLink } from "./whyAgentsFailView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("Pages that lead to /maxim (N77)", () => {
	it("Why agents fail links the failures pulled from Maxim", () => {
		const html = renderToStaticMarkup(<MaximFailuresLink />);
		expect(html).toContain('href="/maxim"');
		expect(html).toContain("Failures from Maxim");
	});
	it("Settings > Integrations > Maxim links them once connected", () => {
		const conn = { connected: true, workspace_id: "w", repo_id: "r", auto_push: true, pushed: 1, failed: 0 } as unknown as MaximConnection;
		const noop = () => undefined;
		const html = renderToStaticMarkup(
			<MaximForm conn={conn} draft={maximDraft(conn)} onDraft={noop} onSave={noop} onTest={noop} onDisconnect={noop} />,
		);
		expect(html).toContain('href="/maxim"');
	});
});