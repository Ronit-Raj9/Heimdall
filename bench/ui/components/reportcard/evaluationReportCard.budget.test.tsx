import "@/lib/i18n";

import { okQuery, render, tagOf } from "@/lib/testing/render";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it, vi } from "vitest";
import { scoreReport } from "./reportCard.fixtures";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/evaluationsApi", async (orig) => ({
	...(await orig<object>()),
	useGetEvaluationReportQuery: vi.fn(() => okQuery(scoreReport)),
	useRaiseEvaluationBudgetMutation: vi.fn(() => [() => ({ unwrap: () => Promise.resolve() }), { isLoading: false }]),
}));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...(await orig<object>()),
	useListRunsQuery: vi.fn(() => ({ data: undefined })),
}));
vi.mock("@/lib/store/apis/failurePatternsApi", async (orig) => ({
	...(await orig<object>()),
	useGetEvaluationFailurePatternsQuery: vi.fn(() => okQuery({ groups: [] })),
}));

// The failures block below the card has its own queries and tests; the headline and the raise panel are real.
vi.mock("./reportFailures", () => ({ ReportFailures: () => null }));

const { EvaluationReportCard } = await import("./evaluationReportCard");

const capped = {
	id: "ev_1",
	status: "done",
	spec: { name: "Capped", mode: "score", arms: [{ label: "A", agent: "codex", version: 2, model: "gpt-6.2-sol" }], benchmarks: ["hd-harbor-tb2"], budget_usd: 5 },
	children: [
		{ benchmark: "hd-harbor-tb2", experiment_id: "e1", status: "done", budget_usd: 5, spent_usd: 4.6, budget_stopped: true, not_started: 13 },
	],
} as unknown as Evaluation;

describe("EvaluationReportCard budget stop (M3-4, G4-01)", () => {
	it("leads with the budget sentence and puts the raise field right under it", () => {
		const html = render(<EvaluationReportCard evaluationId="ev_1" evaluation={capped} />);
		// The fixture counts 40 runs; 13 did not start, so 53 were planned.
		const sentence = "Stopped by the $5.00 budget after 40 of 53 runs";
		expect(html).toContain(sentence);
		expect(html).toContain('data-testid="evaluation-budget-stopped"');
		expect(html.indexOf(sentence)).toBeLessThan(html.indexOf('data-testid="evaluation-budget-stopped"'));
		expect(html).toContain("The budget stopped 1 benchmark: 13 runs not started.");
		expect(html).toContain("Raise budget, up to $10");
		// The suggested new total doubles the stopped cap: $5 now, $10 suggested.
		expect(tagOf(html, "evaluation-raise-budget-input")).toContain('value="10"');
	});

	it("shows no budget sentence and no raise field when nothing was stopped", () => {
		const free = { ...capped, children: [{ ...capped.children![0], budget_stopped: false, not_started: 0 }] } as Evaluation;
		const html = render(<EvaluationReportCard evaluationId="ev_1" evaluation={free} />);
		expect(html).not.toContain("Stopped by the");
		expect(html).not.toContain('data-testid="evaluation-budget-stopped"');
	});
});
