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
	useRaiseEvaluationBudgetMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
}));

vi.mock("@/components/releases/promoteEvaluation", () => ({
	PromoteEvaluation: ({ evaluationId }: { evaluationId: string }) => <div data-testid="promote-mock">{evaluationId}</div>,
	RepeatEvaluation: () => <div data-testid="repeat-mock" />,
	GateResultCard: ({ gate }: { gate: { state: string } }) => <div data-testid="gate-mock">{gate.state}</div>,
}));

vi.mock("@/lib/store/apis/dataOpsApi", () => ({ useDeleteEvaluationMutation: () => [() => ({ unwrap: () => Promise.resolve() }), {}] }));

vi.mock("@/lib/store/apis/runControlApi", async (orig) => ({
	...(await orig<object>()),
	usePauseEvaluationMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
	useResumeEvaluationMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
	useRerunEvaluationFailuresMutation: vi.fn(() => [() => undefined, { isLoading: false }]),
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

describe("EvaluationBody delete (X16)", () => {
	it("disables Delete while the evaluation runs and enables it once done", async () => {
		const { hostedSession } = await import("@/lib/testing/platformFixtures");
		const { tagOf } = await import("@/lib/testing/render");
		const running = render(<EvaluationBody evaluation={base} />, hostedSession("admin"));
		expect(tagOf(running, "evaluation-delete")).toContain('disabled=""');
		expect(running).toContain("Cancel the evaluation or wait for it to finish before deleting it.");
		const done = render(<EvaluationBody evaluation={{ ...base, status: "done" }} />, hostedSession("admin"));
		expect(tagOf(done, "evaluation-delete")).not.toContain('disabled=""');
		expect(render(<EvaluationBody evaluation={{ ...base, status: "done" }} />, hostedSession("viewer"))).not.toContain('data-testid="evaluation-delete"');
	});
});

describe("EvaluationBody budget (G4-01)", () => {
	it("shows spend per benchmark, the runs the cap stopped and Raise budget and continue", () => {
		const html = render(
			<EvaluationBody
				evaluation={{
					...base,
					status: "done",
					children: [
						{ benchmark: "hd-harbor-tb2", experiment_id: "e1", status: "failed", budget_usd: 5, spent_usd: 4.6, budget_stopped: true, not_started: 13 },
						{ benchmark: "tau2", experiment_id: "e2", status: "done", budget_usd: 5, spent_usd: 2.1 },
					],
				}}
			/>,
		);
		expect(html).toContain("Stopped at the $5.00 budget: 13 runs not started");
		expect(html).toContain("$2.10 of $5.00 spent");
		expect(html).toContain("The budget stopped 1 benchmark: 13 runs not started.");
		expect(html).toContain("Raise budget and continue");
		expect(html).toContain('value="10"');
	});

	it("shows no raise panel when nothing was stopped", () => {
		const html = render(<EvaluationBody evaluation={base} />);
		expect(html).not.toContain('data-testid="evaluation-budget-stopped"');
	});
});
