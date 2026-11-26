import "@/lib/i18n";

import { mutation } from "@/lib/testing/render";
import type { EvaluationReleasePreview } from "@/lib/store/apis/evalReleaseApi";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
const previewHook = vi.fn();
vi.mock("@/lib/store/apis/evalReleaseApi", () => ({
	useGetEvaluationReleasePreviewQuery: (...a: unknown[]) => previewHook(...a),
	useReleaseEvaluationMutation: () => mutation(),
	useScheduleEvaluationMutation: () => mutation(),
}));

const { previewTitle } = await import("./promoteEvaluation");

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

describe("promote preview title on shared tasks (w4m2, p04-6)", () => {
	it("names the tasks both evaluations ran when the baseline is the same version", () => {
		const t = (k: string) => k;
		const same: EvaluationReleasePreview = {
			...preview,
			replaces_version: 4,
			baseline: { agent: "codex", version: 4, arm: "A", live: true },
			summary: {
				...preview.summary!,
				dataset: {
					changed: false,
					summary: "",
					benchmarks: [
						{ benchmark: "a", changed: false, compared: 2, new_tasks: 0, dropped_tasks: 0 },
						{ benchmark: "b", changed: false, compared: 1, new_tasks: 0, dropped_tasks: 0 },
					],
				},
			},
		};
		expect(previewTitle(same, t)).toContain("on the 3 tasks both ran");
	});
});