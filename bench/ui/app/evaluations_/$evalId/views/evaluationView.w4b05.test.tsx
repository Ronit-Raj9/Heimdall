import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/components/evaluations/paidCaps", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/components/evaluations/paidCaps")>()),
	useRerunEvaluationFailuresCappedMutation: () => [async () => ({}), { isLoading: false }],
	useStartCheatCheckCappedMutation: () => [async () => ({}), { isLoading: false }],
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/components/reportcard/evaluationReportCard", () => ({
	EvaluationReportCard: ({ evaluationId }: { evaluationId: string }) => <div data-testid="report-card-mock">{evaluationId}</div>,
}));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...(await orig<object>()),
	useGetExperimentQuery: vi.fn(() => okQuery({ exp_id: "e1", slots: { total: 20, done: 7, running: 2, failed: 0 } })),
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (orig) => ({
	...(await orig<object>()),
	useGetEvaluationQuery: vi.fn(),
	// Lane RB: the page reads the report (subtitle totals) and the release preview (summary line).
	useGetEvaluationReportQuery: vi.fn(() => ({ data: undefined })),
	useCancelEvaluationMutation: vi.fn(() => [() => ({ unwrap: () => Promise.resolve() }), { isLoading: false }]),
	useRaiseEvaluationBudgetMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
}));

vi.mock("@/components/releases/promoteEvaluation", () => ({
	PromoteEvaluation: ({ evaluationId }: { evaluationId: string }) => <div data-testid="promote-mock">{evaluationId}</div>,
	RepeatEvaluation: () => <div data-testid="repeat-mock" />,
	GateResultCard: ({ gate }: { gate: { state: string } }) => <div data-testid="gate-mock">{gate.state}</div>,
}));

vi.mock("@/lib/store/apis/evalReleaseApi", async (orig) => ({
	...(await orig<object>()),
	useGetEvaluationReleasePreviewQuery: vi.fn(() => ({ data: undefined })),
}));
vi.mock("@/lib/store/apis/dataOpsApi", () => ({ useDeleteEvaluationMutation: () => [() => ({ unwrap: () => Promise.resolve() }), {}] }));

vi.mock("@/lib/store/apis/runControlApi", async (orig) => ({
	...(await orig<object>()),
	usePauseEvaluationMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
	useResumeEvaluationMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
	useRerunEvaluationFailuresMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
}));

vi.mock("@/lib/store/apis/agentopsApi", async (orig) => ({
	...(await orig<object>()),
	useScanAgentSkillMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
}));

vi.mock("@/lib/store/apis/bifrostUseApi", async (orig) => ({
	...(await orig<object>()),
	useGetEvaluationBifrostUseQuery: vi.fn(() => ({ data: undefined })),
}));

const { EvaluationBody } = await import("./evaluationView");

const base: Evaluation = {
	id: "ev_fl",
	spec: {
		name: "First look",
		mode: "versions",
		preset: "first_look",
		arms: [{ label: "A", agent: "codex", version: 1, model: "gpt-x" }],
		benchmarks: ["hd-harbor-tb2"],
		tasks_per_benchmark: 2,
		trials: 1,
		faults: ["clean"],
	},
	status: "running",
	children: [{ benchmark: "hd-harbor-tb2", experiment_id: "e1", status: "running" }],
	created_at: "2026-11-20T10:00:00Z",
};

describe("evaluation page first look note (w4b05)", () => {
	it("shows the evaluation's task count", () => {
		const html = render(<EvaluationBody evaluation={base} />);
		expect(html).toContain('data-testid="first-look-note"');
		expect(html).toContain("First look: 2 tasks, too few for a verdict.");
	});
	it("shows no note when the task count is not known", () => {
		const html = render(<EvaluationBody evaluation={{ ...base, spec: { ...base.spec, tasks_per_benchmark: 0 } }} />);
		expect(html).not.toContain('data-testid="first-look-note"');
	});
});