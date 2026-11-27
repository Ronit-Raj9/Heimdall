import "@/lib/i18n";

import { ShadowReplayLink } from "@/components/replays/shadowReplayLink";
import { BENCH_NAV_GROUPS } from "@/lib/constants/navigation";
import type { ReplayReport, ReplaySession } from "@/lib/store/apis/replaysApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { RunSummary, SessionRow, SettingsToggle, replayJobParams, splitModel, stateBar, type ReplayForm } from "./replaysView";

vi.mock("@/components/models/groupedModelPicker", () => ({
	GroupedModelPicker: ({ testId, value }: { testId?: string; value: { model: string } | null }) => (
		<div data-testid={testId} data-model={value?.model ?? ""} />
	),
}));


vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const noop = () => undefined;

const form: ReplayForm = {
	source: "prod",
	window: "7d",
	sessions: "10",
	onlyFailed: true,
	mode: "trajectory",
	candidate: "openai/gpt-4o-mini",
	judge: "",
	cap: "2",
	yes: true,
};

describe("shadow replay page", () => {
	it("is reached from Comparisons (Spec 1): the menu item covers /replays and the page links it", () => {
		const why = BENCH_NAV_GROUPS.flatMap((g) => g.items).find((i) => i.to === "/experiments");
		expect(why?.also).toContain("/replays");
		const html = renderToStaticMarkup(<ShadowReplayLink />);
		expect(html).toContain('href="/replays"');
		expect(html).toContain("Shadow replay");
	});

	it("builds the job params, with the cost confirmation and the cap", () => {
		expect(replayJobParams(form)).toEqual({
			source: "prod",
			since: "7d",
			sessions: 10,
			only_failed: true,
			mode: "trajectory",
			candidate: { provider: "openai", model: "gpt-4o-mini", budget_usd: 2, yes: true },
		});
		expect(replayJobParams({ ...form, candidate: "gpt-4o-mini" })).toBeNull();
		expect(replayJobParams({ ...form, judge: "nope" })).toBeNull();
		expect(splitModel("anthropic/claude-x")).toEqual({ provider: "anthropic", model: "claude-x" });
	});

	it("shows the opt-in off with the plain refusal, and asks before turning it on", () => {
		const html = renderToStaticMarkup(
			<SettingsToggle
				settings={{ enabled: false, enabled_by: "", enabled_at: null, max_sessions_per_run: 50, max_usd_per_run: 10 }}
				onSave={noop}
				admin={false}
			/>,
		);
		expect(html).toContain("Shadow replay: Off");
		expect(html).toContain("Shadow replay is off. Ask an org admin to turn it on.");
		expect(html).not.toContain('role="switch"');
		const asAdmin = renderToStaticMarkup(
			<SettingsToggle settings={{ enabled: false, enabled_by: "", enabled_at: null, max_sessions_per_run: 50, max_usd_per_run: 10 }} onSave={noop} />,
		);
		expect(asAdmin).toMatch(/role="switch"[^>]*aria-checked="false"/);
		expect(asAdmin).toContain("Allow shadow replay for this organization");
		expect(asAdmin).toContain("Turn on");
	});

	it("puts the states in a fixed order with their words", () => {
		expect(stateBar({ different_tool_call: 1, equivalent_response: 3 }).map((p) => p.state)).toEqual([
			"equivalent_response",
			"different_tool_call",
		]);
		expect(stateBar({})).toEqual([]);
		const report: ReplayReport = {
			id: "j1",
			mode: "fixed",
			candidate: "openai/gpt-4o-mini",
			recorded: ["openai/gpt-4o"],
			sessions: [],
			counts: { equivalent_response: 8, different_response: 2, redacted_input: 1 },
			spent_usd: 0.2,
			note: "A replay comparison of recorded sessions. It is not a benchmark result: nothing was run for real and nothing was graded by a task's tests.",
			stats: {
				compared: 10,
				left_out: 1,
				shares: { equivalent_response: 0.8 },
				equivalent_low: 0.49,
				equivalent_high: 0.943,
				paired: 0,
				median_cost_usd: 0,
				median_recorded_cost_usd: 0,
				median_latency_ms: 0,
				median_recorded_latency_ms: 0,
				paired_latency: 0,
			},
			created_at: "2026-11-20T10:00:00Z",
		};
		const html = renderToStaticMarkup(<RunSummary report={report} />);
		expect(html).toContain("Same answer: 8");
		expect(html).toContain("Not sent: the recorded input was cleaned of private data: 1");
		expect(html).toContain("Same answer in 80% of 10 compared steps (95% interval 49% to 94%)");
		expect(html).toContain("It is not a benchmark result");
		expect(html).not.toMatch(/ship/i);
	});

	it("offers Send to Inbox only on sessions that are not the same", () => {
		const s: ReplaySession = {
			recording: "rec_1",
			session_id: "s1",
			steps: [],
			final: "missing_recorded_tool_result",
			diverged_at: 2,
			write_set_equal: false,
			cost_usd: 0,
		};
		const html = renderToStaticMarkup(<SessionRow session={s} n={1} onInbox={noop} />);
		expect(html).toContain("Stopped: no recorded result for this tool call");
		expect(html).toContain("stopped at step 3");
		expect(html).toContain("write calls differ");
		expect(html).toContain("Send to Inbox");
		const same = renderToStaticMarkup(<SessionRow session={{ ...s, final: "equivalent_response" }} n={1} onInbox={noop} />);
		expect(same).not.toContain("Send to Inbox");
	});
});