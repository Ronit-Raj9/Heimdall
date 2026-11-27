import "@/lib/i18n";

import { mutation, okQuery, render } from "@/lib/testing/render";
import type { EvaluationReleasePreview } from "@/lib/store/apis/evalReleaseApi";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
const previewHook = vi.fn();
vi.mock("@/lib/store/apis/evalReleaseApi", () => ({
	useGetEvaluationReleasePreviewQuery: (...a: unknown[]) => previewHook(...a),
	useReleaseEvaluationMutation: () => mutation(),
	useScheduleEvaluationMutation: () => mutation(),
}));

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useGetEvaluationQuery: () => ({ data: { result: { arms: [], cost_usd: 0.4, at: "" } } }),
}));

const { GateResultCard, PromoteEvaluation, RepeatEvaluation, onlyLiveVersion, parseRepeatLimit, previewTitle, repeatAsk } = await import("./promoteEvaluation");
const { releaseTitle } = await import("./releaseEvidence");

const preview: EvaluationReleasePreview = {
	evaluation: "ev_1",
	status: "done",
	mode: "versions",
	agent: "codex",
	version: 4,
	replaces_version: 3,
	live: { agent: "codex", version: 3, release_id: "rel_1" },
	candidate: "B",
	baseline: { agent: "codex", version: 3, arm: "A", live: true },
	summary: {
		candidate: { label: "B", agent: "codex", version: 4, score: 0.6, ci_low: 0.4, ci_high: 0.8, runs: 16 },
		baseline: { label: "A", agent: "codex", version: 3, score: 0.5, ci_low: 0.3, ci_high: 0.7, runs: 16 },
	},
	decision: "inconclusive",
	children: [{ benchmark: "hd-harbor-tb2", experiment_id: "e1", status: "done", included: true }],
	blocked: [],
	ready: true,
};

describe("PromoteEvaluation", () => {
	it("titles the release v4 replaces v3, shows the live version, numbers and the Promote button", () => {
		previewHook.mockReturnValue(okQuery(preview));
		const html = render(<PromoteEvaluation evaluationId="ev_1" />);
		expect(html).toContain("codex v4 replaces v3");
		expect(html).toContain("Live now: codex v3");
		expect(html).toContain('data-testid="evaluation-promote-numbers"');
		expect(html).toContain('data-testid="evaluation-promote-open"');
		expect(html).not.toContain('data-testid="evaluation-promote-blocked"');
	});

	it("lists what blocks the release and hides the button when it cannot be released at all", () => {
		previewHook.mockReturnValue(okQuery({ ...preview, blocked: ["Gate failed: terminal fell from 50% to 30%"], ready: false }));
		expect(render(<PromoteEvaluation evaluationId="ev_1" />)).toContain("terminal fell from 50% to 30%");
		previewHook.mockReturnValue(okQuery({ ...preview, problem: "codex v4 is already the live version" }));
		const html = render(<PromoteEvaluation evaluationId="ev_1" />);
		expect(html).toContain("already the live version");
		expect(html).not.toContain('data-testid="evaluation-promote-open"');
	});

	it("names a first release", () => {
		const t = (k: string, o?: Record<string, unknown>) => `${k}:${o?.name}`;
		expect(previewTitle({ ...preview, replaces_version: 0 }, t)).toBe("releases.first:codex v4");
	});

	it("does not say a version replaces itself (A4.gate)", () => {
		const t = (k: string, o?: Record<string, unknown>) => `${k}:${o?.name}`;
		const same = { agent_id: "codex", version: 1, replaces_version: 0, compared_version: 1, agent_config_id: "", arm: "A" };
		expect(releaseTitle(same, t)).toBe("codex v1, compared with an earlier evaluation of the same version");
		expect(releaseTitle({ ...same, replaces_version: 1 }, t)).toBe("codex v1, compared with an earlier evaluation of the same version");
		expect(releaseTitle({ ...same, version: 2, replaces_version: 1 }, t)).toBe("releases.replaces:codex v2");
		expect(releaseTitle({ ...same, compared_version: undefined }, t)).toBe("releases.first:codex v1");
	});
});

describe("GateResultCard and RepeatEvaluation", () => {
	it("says when the benchmark changed since the baseline and what was compared", () => {
		const gate = {
			state: "pass" as const,
			agent: "codex",
			candidate: "B",
			checks: [],
			summary: "Gate passed",
			decided_at: "2026-11-20T11:00:00Z",
			dataset: {
				changed: true,
				summary: "Benchmark changed since baseline",
				benchmarks: [
					{ benchmark: "support", changed: true, compared: 40, new_tasks: 15, dropped_tasks: 0 },
					{ benchmark: "faq", changed: false, compared: 10, new_tasks: 0, dropped_tasks: 0 },
					{ benchmark: "billing", changed: true, compared: 0, new_tasks: 5, dropped_tasks: 0, not_compared: true },
				],
			},
		};
		const html = render(<GateResultCard gate={gate} />);
		expect(html).toContain("Benchmark changed since baseline");
		expect(html).toContain("compared on the 40 tasks both ran (15 new or changed, 0 no longer run)");
		expect(html).toContain('data-testid="evaluation-gate-dataset-billing"');
		expect(html).not.toContain('data-testid="evaluation-gate-dataset-faq"');
		const same = render(<GateResultCard gate={{ ...gate, dataset: { ...gate.dataset, changed: false } }} />);
		expect(same).not.toContain("Benchmark changed since baseline");
	});
	it("shows pass or fail with each check", () => {
		const html = render(
			<GateResultCard
				gate={{
					state: "fail",
					agent: "codex",
					candidate: "B",
					baseline: { agent: "codex", version: 3, live: true },
					checks: [
						{
							capability: "terminal",
							rule: "max_regression",
							threshold: 0.05,
							observed: 0.3,
							baseline: 0.5,
							pass: false,
							summary: "terminal fell from 50% to 30%",
						},
					],
					summary: "Gate failed: terminal fell from 50% to 30%",
					decided_at: "2026-11-20T11:00:00Z",
				}}
			/>,
		);
		expect(html).toContain('data-state="fail"');
		expect(html).toContain("Failed");
		expect(html).toContain("compared with the live version codex v3");
		expect(html).toContain('data-testid="evaluation-gate-check-max_regression-terminal"');
	});

	it("shows an inconclusive check with its paired interval and detectable size", () => {
		const html = render(
			<GateResultCard
				gate={{
					state: "inconclusive",
					agent: "codex",
					candidate: "B",
					checks: [
						{
							capability: "support",
							rule: "max_regression",
							threshold: 0.05,
							observed: 0.5,
							baseline: 0.62,
							pass: false,
							inconclusive: true,
							tasks: 8,
							change: -0.12,
							change_low: -0.38,
							change_high: 0.12,
							mde: 0.35,
							notes: ["support: the baseline ran 8 of these 10 tasks; 2 new tasks not compared"],
							summary: "support fell from 62% to 50% on 8 paired tasks, over the 5% allowed, but the drop could be noise",
						},
					],
					summary: "Gate inconclusive",
					decided_at: "2026-11-20T11:00:00Z",
				}}
			/>,
		);
		expect(html).toContain('data-state="inconclusive"');
		expect(html).toContain("Inconclusive");
		expect(html).toContain("Change -12 pts on 8 paired tasks (95% interval -38 pts to +12 pts)");
		expect(html).toContain("this size can detect about 35 points");
		expect(html).toContain("2 new tasks not compared");
	});

	it("offers nightly and weekly repeats", () => {
		const html = render(<RepeatEvaluation evaluationId="ev_1" />);
		expect(html).toContain('data-testid="evaluation-repeat-nightly"');
		expect(html).toContain("Repeat weekly");
		expect(html).toContain('data-testid="evaluation-repeat-limit"');
		expect(html).toContain('data-paid="true"');
	});

	it("asks before a repeat run with the time, the zone and the cost (M7-51)", async () => {
		const i18n = (await import("@/lib/i18n")).default;
		const t = i18n.getFixedT("en", "uf7b") as never;
		const ask = repeatAsk("nightly", "Europe/Berlin", 0.4, t);
		expect(ask.message).toBe("It runs again every night at 02:00 (Europe/Berlin). About $0.40 a run (what this run cost), up to $12.00 a month. Daily limit: $0.80.");
		expect(ask.limit).toBe(0.8);
		const unknown = repeatAsk("weekly", "UTC", undefined, t);
		expect(unknown.message).toContain("every Monday at 02:00 (UTC)");
		expect(unknown.message).toContain("Price per run not known");
		expect(unknown.limit).toBeUndefined();
		const typed = repeatAsk("nightly", "UTC", 0.4, t, 5);
		expect(typed.message).toContain("Daily limit: $5.00.");
		expect(typed.limit).toBe(5);
		const unknownTyped = repeatAsk("weekly", "UTC", undefined, t, 3);
		expect(unknownTyped.message).toContain("Daily limit: $3.00.");
		expect(unknownTyped.limit).toBe(3);
		expect(parseRepeatLimit("")).toBeUndefined();
		expect(parseRepeatLimit("$2.5")).toBe(2.5);
		expect(parseRepeatLimit("0")).toBeNaN();
		expect(parseRepeatLimit("abc")).toBeNaN();
	});

	it("knows when the evaluated version is already live (M7-74)", () => {
		expect(onlyLiveVersion({ problem: "codex v1 is already the live version", version: 1, live: { agent: "codex", version: 1 } })).toBe(true);
		expect(onlyLiveVersion({ problem: "", version: 1, live: { agent: "codex", version: 1 } })).toBe(false);
		expect(onlyLiveVersion({ problem: "x", version: 2, live: { agent: "codex", version: 1 } })).toBe(false);
		expect(onlyLiveVersion({ problem: "x", problem_code: "same_as_live", version: 2, live: { agent: "codex", version: 1 } })).toBe(true);
		expect(onlyLiveVersion({ problem: "x", problem_code: "only_one_version", version: 2, live: { agent: "codex", version: 1 } })).toBe(true);
		expect(onlyLiveVersion({ problem: "x", problem_code: "not_finished", version: 1, live: { agent: "codex", version: 1 } })).toBe(false);
	});
});

describe("PromoteEvaluation says why there is no button (J7.promote)", () => {
	it("tells a viewer and a demo visitor that only members promote", async () => {
		const { demoSession, hostedSession } = await import("@/lib/testing/platformFixtures");
		previewHook.mockReturnValue(okQuery(preview));
		for (const session of [hostedSession("viewer"), demoSession()]) {
			const html = render(<PromoteEvaluation evaluationId="ev_1" />, session);
			expect(html).not.toContain('data-testid="evaluation-promote-open"');
			expect(html).toContain('data-testid="evaluation-promote-why"');
			expect(html).toContain("Only members of the organization can promote.");
		}
	});
	it("gives the problem as the reason and no members note to a member", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		previewHook.mockReturnValue(okQuery({ ...preview, problem: "codex v4 is already the live version" }));
		const html = render(<PromoteEvaluation evaluationId="ev_1" />, hostedSession("member"));
		expect(html).not.toContain('data-testid="evaluation-promote-open"');
		expect(html).toContain("codex v4 is already the live version");
		expect(html).not.toContain("Only members of the organization can promote.");
	});
});