import "@/lib/i18n";

import type { AgentCard } from "@/lib/store/apis/agentCardApi";
import { agentCardRequest, unwrapAgentCard } from "@/lib/store/apis/agentCardApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import ReportCardTab from "./reportCardTab";

// Fix wave 3, N206: the agent card names the one evaluation it shows, says how many others match and
// lets the reader pick one (?evaluation=).

const state = vi.hoisted(() => ({
	card: undefined as unknown,
	args: [] as unknown[],
	search: {} as Record<string, unknown>,
	navigated: [] as unknown[],
}));

vi.mock("@/lib/store/apis/agentCardApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/agentCardApi")>()),
	useGetAgentCardQuery: (arg: unknown) => {
		state.args.push(arg);
		return { data: state.card, isLoading: false, isError: false };
	},
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useGetAgentVersionsQuery: () => ({ data: { agent: "codex", versions: [] }, isLoading: false, isError: false }),
}));
vi.mock("@/components/reportcard/reportCard", () => ({ ReportCard: () => <div data-testid="report-card" /> }));
vi.mock("@/components/reportcard/reportFailures", () => ({ ReportFailures: () => null }));
vi.mock("@/components/share/shareDialog", () => ({ ShareButton: () => null, shareQuery: () => undefined }));
vi.mock("@tanstack/react-router", () => ({
	useSearch: () => state.search,
	useNavigate: () => (opts: unknown) => state.navigated.push(opts),
	Link: ({ to, children }: { to: string; children: React.ReactNode }) => <a href={to}>{children}</a>,
}));

const card = (over: Partial<AgentCard> = {}): AgentCard => ({
	evaluation: "ev_new",
	arms: [{ label: "A", agent: "codex", version: 1, model: "gpt-x" }],
	capabilities: [{ capability: "coding", benchmarks: ["coding-core"], arms: { A: { score: 0.5, ci_low: 0.3, ci_high: 0.7, runs: 5 } } }],
	benchmarks: [],
	reliability: [],
	totals: { A: { score: 0.5, ci_low: 0.3, ci_high: 0.7, runs: 5, solved: 2, cost_usd: null } },
	evaluation_id: "ev_new",
	evaluation_name: "codex: two models",
	evaluation_created_at: "2026-11-22T12:00:00Z",
	matching_evaluations: 3,
	evaluations: [
		{ id: "ev_new", name: "codex: two models", created_at: "2026-11-22T12:00:00Z" },
		{ id: "ev_mid", name: "codex first look", created_at: "2026-11-21T12:00:00Z" },
		{ id: "ev_old", created_at: "2026-11-20T12:00:00Z" },
	],
	...over,
});

describe("agent report card tab names its evaluation (N206)", () => {
	it("says which evaluation it shows, its runs and how many older ones match", () => {
		state.card = card();
		state.search = {};
		const html = renderToStaticMarkup(<ReportCardTab agentId="codex" />);
		expect(html).toContain('data-testid="agent-report-card-source"');
		expect(html).toContain("From the newest matching evaluation, codex: two models");
		expect(html).toContain(": 5 runs. 2 older evaluations match; pick one below.");
		expect(html).toContain('data-testid="agent-report-card-evaluation"');
		expect(html).toMatch(/<option value="ev_mid">codex first look/);
		expect(html).toMatch(/<option value="ev_old">ev_old/);
		// One "Showing: ... Change" line holds the picks (M2-34).
		expect(html).toContain('data-testid="agent-report-card-picks-summary"');
		expect(html).toMatch(/agent-report-card-picks-summary">codex: two models, [^<]+ \(5 runs\)</);
		expect(html).toContain('data-testid="agent-report-card-picks-change"');
	});
	it("reads ?evaluation= into the request and the select, and writes a pick back", () => {
		state.card = card({ evaluation_id: "ev_mid", evaluation_name: "codex first look" });
		state.search = { evaluation: "ev_mid" };
		state.args = [];
		const html = renderToStaticMarkup(<ReportCardTab agentId="codex" />);
		expect(state.args).toContainEqual({ agent: "codex", evaluation: "ev_mid" });
		expect(html).toMatch(/<option value="ev_mid" selected="">codex first look/);
		expect(html).toContain("From the evaluation codex first look");
		expect(html).toContain("2 other evaluations match");
		state.search = {};
	});
	it("has no picker when only one evaluation matches", () => {
		state.card = card({
			matching_evaluations: 1,
			evaluations: [{ id: "ev_new", name: "codex: two models", created_at: "2026-11-22T12:00:00Z" }],
		});
		const html = renderToStaticMarkup(<ReportCardTab agentId="codex" />);
		expect(html).toContain("From the only matching evaluation, codex: two models");
		expect(html).not.toContain('data-testid="agent-report-card-evaluation"');
	});
	it("sends evaluation only when set and keeps the card facts", () => {
		expect(agentCardRequest({ agent: "x" })).toEqual({ url: "/agents/x/report-card" });
		expect(agentCardRequest({ agent: "x", model: "m", evaluation: "ev_1" })).toEqual({
			url: "/agents/x/report-card",
			params: { model: "m", evaluation: "ev_1" },
		});
		const out = unwrapAgentCard({
			agent: "x",
			report: null,
			evaluation_id: "ev_1",
			matching_evaluations: 2,
			evaluations: [{ id: "ev_1", created_at: "" }],
		});
		expect(out.evaluation).toBe("");
		expect(out.evaluation_id).toBe("ev_1");
		expect(out.matching_evaluations).toBe(2);
		expect(out.evaluations).toHaveLength(1);
	});
});