import "@/lib/i18n";

import { abReport } from "@/components/reportcard/reportCard.fixtures";
import { errorQuery, okQuery, render } from "@/lib/testing/render";
import { signedOutSession } from "@/lib/testing/platformFixtures";
import type { SharedItem } from "@/lib/store/apis/shareApi";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SharedView, { replaceShareAddress, SharedItemView } from "./sharedView";

const api = vi.hoisted(() => ({ shared: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/shareApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/shareApi")>()),
	useGetSharedQuery: () => api.shared,
}));

const evaluationItem: SharedItem = {
	share: {
		kind: "evaluation",
		target: "ev_1",
		label: "Board review",
		hide_prompts: true,
		org: "Acme Robotics",
		created_at: "2026-11-22T00:00:00Z",
		expires_at: "2026-12-22T00:00:00Z",
		exports: ["html", "md"],
		badge: true,
	},
	data: {
		evaluation: { id: "ev_1", spec: { name: "Codex v2 check" } },
		report: abReport,
	},
};

beforeEach(() => {
	api.shared = okQuery(evaluationItem);
});

describe("SharedView", () => {
	it("shows a shared evaluation read-only, with its exports and no links into the org", () => {
		const html = render(<SharedView token="org_a.shr_x.sig" />, signedOutSession());
		expect(html).toContain('data-testid="shared-evaluation"');
		expect(html).toContain("Shared by Acme Robotics");
		expect(html).toContain("Board review");
		expect(html).toContain('data-testid="report-card"');
		expect(html).toContain('data-testid="shared-prompts-hidden"');
		expect(html).toContain("/api/bench/shared/org_a.shr_x.sig/export?format=html");
		expect(html).not.toContain("benchmark-link-");
		expect(html).not.toContain("report-copy-link");
	});

	it("tells the visitor when a link expired or never existed", () => {
		api.shared = errorQuery(410, "expired");
		expect(render(<SharedView token="t" />, signedOutSession())).toContain("expired or was turned off");
		api.shared = errorQuery(404, "nope");
		expect(render(<SharedView token="t" />, signedOutSession())).toContain("does not exist");
	});

	it("renders a shared leaderboard and run", () => {
		const board: SharedItem = {
			share: {
				kind: "leaderboard",
				target: "agents",
				hide_prompts: false,
				created_at: "2026-11-22T00:00:00Z",
				badge: false,
			},
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
							capabilities: {
								coding: { score: 0.5, ci_low: 0.3, ci_high: 0.7, runs: 8 },
							},
							cost_usd: 1.5,
							last_evaluation: "ev_1",
						},
					],
				},
			},
		};
		const html = render(<SharedItemView item={board} token="t" />, signedOutSession());
		expect(html).toContain('data-testid="shared-agent-board"');
		expect(html).toContain("codex v2");
		expect(html).toContain("50%");
	});
});
describe("replaceShareAddress (fix wave 3, N293)", () => {
	it("moves an old token's address to the opaque token, keeping query and hash", () => {
		const replaceState = vi.fn();
		const win = { history: { state: { k: 1 }, replaceState }, location: { pathname: "/s/org_a.shr_x.sig", search: "?x=1", hash: "#top" } };
		replaceShareAddress("s_abc", win as unknown as Window);
		expect(replaceState).toHaveBeenCalledWith({ k: 1 }, "", "/s/s_abc?x=1#top");
	});

	it("does nothing when the address is already the opaque one", () => {
		const replaceState = vi.fn();
		replaceShareAddress("s_abc", {
			history: { state: null, replaceState },
			location: { pathname: "/s/s_abc", search: "", hash: "" },
		} as unknown as Window);
		expect(replaceState).not.toHaveBeenCalled();
	});
});