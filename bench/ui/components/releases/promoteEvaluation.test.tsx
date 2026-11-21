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

const { GateResultCard, PromoteEvaluation, RepeatEvaluation, previewTitle } = await import("./promoteEvaluation");

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
});

describe("GateResultCard and RepeatEvaluation", () => {
	it("shows pass or fail with each check", () => {
		const html = render(
			<GateResultCard
				gate={{
					state: "fail",
					agent: "codex",
					candidate: "B",
					baseline: { agent: "codex", version: 3, live: true },
					checks: [{ capability: "terminal", rule: "max_regression", threshold: 0.05, observed: 0.3, baseline: 0.5, pass: false, summary: "terminal fell from 50% to 30%" }],
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

	it("offers nightly and weekly repeats", () => {
		const html = render(<RepeatEvaluation evaluationId="ev_1" />);
		expect(html).toContain('data-testid="evaluation-repeat-nightly"');
		expect(html).toContain("Repeat weekly");
	});
});
