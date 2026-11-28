import "@/lib/i18n";

import { GatewayCardLineRow } from "@/app/gateway/views/gatewayView";
import { render } from "@/lib/testing/render";
import type { GwCardLine, GwEffect } from "@/lib/types/gateway";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { GatewayEffectView } from "./gatewayEffectCard";

vi.mock("@tanstack/react-router", async (orig) => ({
	...((await orig()) as object),
	Link: ({ children }: { children: unknown }) => children,
}));

// The handler's own answers (handlers/gatewaytests_ufg1_test.go writes them).
const read = <T,>(file: string): T => JSON.parse(fs.readFileSync(path.join(__dirname, "testdata", file), "utf8")) as T;
const effects = read<Record<string, GwEffect>>("ufg1.effects.json");
const lines = read<Record<string, GwCardLine>>("ufg1.cardlines.json");
const text = (html: string) => html.replace(/<[^>]+>/g, " ").replace(/&#x27;|&#39;/g, "'").replace(/\s+/g, " ");

describe("feature test states, from the handler's answers", () => {
	it.each([
		["e_running", "Running", "Running, 1 of 3 runs graded."],
		["e_refused", "No result", "No result: no run started: the agent's image could not be built"],
		["e_broken", "Broken", "Broken: the MCP log and the ledger disagree. Fix it and run it again."],
		["e_none", "Not enough data yet", "No setup solved a task, so nothing can be recommended."],
		["e_few", "Not enough data yet", "Too few runs to tell. Nothing is recommended yet."],
		["e_result", "Measured", "Best value: 0.01 USD."],
	])("%s says one line", (id, pill, headline) => {
		const html = text(render(<GatewayEffectView effect={effects[id]} />));
		expect(html).toContain(pill);
		expect(html).toContain(headline);
		if (id !== "e_result") {
			expect(html).not.toMatch(/\b(Saved|Lost|Worked|Failed)\b/);
			expect(html).not.toContain("No verdict:");
		}
		if (id !== "e_broken") expect(html.toLowerCase()).not.toContain("broken");
	});

	it("a running test is never broken and has no other reason line", () => {
		const html = text(render(<GatewayEffectView effect={effects.e_running} />));
		expect(html.toLowerCase()).not.toContain("broken");
		expect(html).not.toContain("No effect shown");
		expect(html).not.toContain("Unknown:");
	});

	it("the card line shows the newest test, and an older result with its date only when it holds", () => {
		const holds = text(render(<GatewayCardLineRow line={lines.older_holds} onStart={() => undefined} />));
		expect(holds).toContain("No result: no run started: the agent's image could not be built");
		expect(holds).toContain("Older result, 2026-11-12: Best value: 0.01 USD.");
		expect(holds).not.toMatch(/\bSaved\b/);
		const html = render(<GatewayCardLineRow line={lines.older_void} onStart={() => undefined} />);
		expect(text(html)).not.toContain("Older result");
		expect(text(html)).not.toContain("0.01 USD");
	});
});
