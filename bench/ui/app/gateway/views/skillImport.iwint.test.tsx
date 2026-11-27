import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { render } from "@/lib/testing/render";
import type { GwCardLine } from "@/lib/types/gateway";
import { describe, expect, it, vi } from "vitest";
import { GatewayCardLineRow } from "./gatewayView";
import { importOutcome } from "./skillImport";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
	useNavigate: () => () => undefined,
}));

// D3 P2 (I24): the Gateway page's skill version line offers the import, and its refusals are shown as the server wrote them.
const t = (k: string, o?: { folder: string }) => String(i18n.t(k as never, { ns: "w4int", ...(o ?? {}) } as never));
const latest =
	"This version has 2 more files, and Heimdall cannot download them from your Bifrost yet, so it can import only versions that hold SKILL.md alone.";

describe("skill import on the Gateway page", () => {
	it("is offered on the skill version line only", () => {
		const line = (feature: string) => ({ feature, name: feature, enabled: "unknown", headline: "h" }) as unknown as GwCardLine;
		expect(render(<GatewayCardLineRow line={line("skill_version")} onStart={() => undefined} />)).toContain("Import a skill from Bifrost");
		expect(render(<GatewayCardLineRow line={line("semantic_cache")} onStart={() => undefined} />)).not.toContain(
			"Import a skill from Bifrost",
		);
	});

	it("shows the latest-version refusal sentence as the server wrote it", () => {
		expect(importOutcome({ error: { status: 400, data: { error: { message: latest, type: "invalid_request" } } } }, t)).toEqual({
			text: latest,
			ok: false,
		});
	});

	it("says whether the safety scan let the skill in", () => {
		expect(importOutcome({ data: { folder: "triage-v3", extra_path: "agents/x/skills/triage-v3", scan: { installed: true } } }, t)).toEqual(
			{
				text: "Imported as triage-v3. Its safety scan let it in.",
				ok: true,
			},
		);
		const blocked = importOutcome({ error: { status: 422, data: { folder: "triage-v3", extra_path: "", scan: { installed: false } } } }, t);
		expect(blocked.ok).toBe(false);
		expect(blocked.text).toContain("Imported as triage-v3, but its safety scan blocked it");
	});
});