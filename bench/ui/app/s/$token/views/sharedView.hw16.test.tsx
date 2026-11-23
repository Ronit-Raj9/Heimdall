import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { signedOutSession } from "@/lib/testing/platformFixtures";
import type { SharedItem } from "@/lib/store/apis/shareApi";
import { describe, expect, it, vi } from "vitest";
import { SharedItemView } from "./sharedView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("shared leaderboard cost (fix wave 3, N207)", () => {
	it("prints a cent-sized cost per run with 4 decimals under the header Cost per run", () => {
		const board: SharedItem = {
			share: { kind: "leaderboard", target: "agents", hide_prompts: false, created_at: "2026-11-22T00:00:00Z", badge: false },
			data: {
				leaderboard: {
					capabilities: ["coding"],
					models: [],
					capability: "",
					model: "",
					generated_at: "",
					rows: [
						{
							agent: "codex",
							version: 2,
							model: "gpt-5",
							label: "",
							baseline: false,
							capabilities: { coding: { score: 0.5, ci_low: 0.3, ci_high: 0.7, runs: 8 } },
							cost_usd: 0.0094,
							last_evaluation: "ev_1",
						},
					],
				},
			},
		};
		const html = render(<SharedItemView item={board} token="t" />, signedOutSession());
		expect(html).toContain("$0.0094");
		expect(html).toContain("Cost per run");
	});
});