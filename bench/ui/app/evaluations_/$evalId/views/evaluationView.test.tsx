import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it, vi } from "vitest";

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
	useCancelEvaluationMutation: vi.fn(() => [() => ({ unwrap: () => Promise.resolve() }), { isLoading: false }]),
}));

vi.mock("@/components/releases/promoteEvaluation", () => ({
	PromoteEvaluation: ({ evaluationId }: { evaluationId: string }) => <div data-testid="promote-mock">{evaluationId}</div>,
	RepeatEvaluation: () => <div data-testid="repeat-mock" />,
	GateResultCard: ({ gate }: { gate: { state: string } }) => <div data-testid="gate-mock">{gate.state}</div>,
}));

const { EvaluationBody } = await import("./evaluationView");

const base: Evaluation = {
	id: "ev_1",
	spec: {
		name: "Codex v1 vs v2",
		mode: "versions",
		arms: [
			{ label: "A", agent: "codex", version: 1, model: "gpt-x" },
			{ label: "B", agent: "codex", version: 2, model: "gpt-x", provider: "openai" },
		],
		benchmarks: ["hd-harbor-tb2", "voicebench"],
		tasks_per_benchmark: 5,
		trials: 2,
		faults: ["clean", "model-down"],
	},
	status: "running",
	children: [
		{ benchmark: "hd-harbor-tb2", experiment_id: "e1", status: "running" },
		{ benchmark: "voicebench", experiment_id: "", status: "skipped", skipped: "agent codex cannot run benchmark voicebench: needs voice" },
	],
	created_at: "2026-11-20T10:00:00Z",
};

describe("EvaluationBody", () => {
	it("shows arms, mode in plain words, progress rows with links, skipped reason and Cancel while running", () => {
		const html = render(<EvaluationBody evaluation={base} />);
		expect(html).toContain("Compare two versions of one agent");
		expect(html).toContain('data-testid="evaluation-arm-A"');
		expect(html).toContain("openai/gpt-x");
		expect(html).toContain("v2");
		expect(html).toContain("Failure tests: model outage");
		expect(html).toContain("7 of 20 runs done");
		expect(html).toContain('href="/experiments/e1"');
		expect(html).toContain('href="/experiments/e1?tab=runs"');
		expect(html).toContain("needs voice");
		expect(html).toContain('data-testid="evaluation-cancel"');
		expect(html).not.toContain('data-testid="report-card-mock"');
	});

	it("renders the report card when done and hides Cancel", () => {
		const html = render(<EvaluationBody evaluation={{ ...base, status: "done", children: [{ ...base.children[0], status: "done" }] }} />);
		expect(html).toContain('data-testid="report-card-mock"');
		expect(html).not.toContain('data-testid="evaluation-cancel"');
		expect(html).toContain("Finished");
	});

	it("shows the gate result, Promote this version and Repeat on a finished evaluation", () => {
		const gate_result = { state: "fail", agent: "codex", candidate: "B", checks: [], summary: "Gate failed", decided_at: "2026-11-20T11:00:00Z" };
		const html = render(<EvaluationBody evaluation={{ ...base, status: "done", gate_result, children: [{ ...base.children[0], status: "done" }] }} />);
		expect(html).toContain('data-testid="gate-mock"');
		expect(html).toContain('data-testid="promote-mock"');
		expect(html).toContain('data-testid="repeat-mock"');
	});

	it("offers no promotion while running", () => {
		const html = render(<EvaluationBody evaluation={base} />);
		expect(html).not.toContain('data-testid="promote-mock"');
		expect(html).not.toContain('data-testid="repeat-mock"');
	});
});
