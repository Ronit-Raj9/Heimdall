import "@/lib/i18n";

import type { Report } from "@/lib/store/apis/evaluationsApi";
import { ReduxProvider } from "@/lib/store/provider";
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

const fx = vi.hoisted(() => ({
	evaluation: undefined as unknown,
	asked: [] as unknown[],
}));

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...((await importOriginal()) as Record<string, unknown>),
	useGetEvaluationQuery: (id: unknown) => {
		fx.asked.push(id);
		return { data: fx.evaluation, isLoading: false, isError: false };
	},
}));

const { ResilienceCard } = await import("./reliabilityCards");
const { ReportCard } = await import("./reportCard");
const { scoreReport } = await import("./reportCard.fixtures");

const inStore = (node: ReactNode) => renderToStaticMarkup(<ReduxProvider>{node}</ReduxProvider>);

const report = {
	evaluation: "ev_d10",
	mode: "score",
	arms: [{ label: "A", agent: "codex", model: "gpt-6.2-sol" }],
	capabilities: [],
	benchmarks: [],
	reliability: [],
	resilience: [
		{
			fault: "model-down",
			arms: {
				A: {
					degradation_ratio: 0.33,
					degradation_ratio_low: 0,
					degradation_ratio_high: 1,
					recovery_rate: 0.33,
					cost_inflation: 0.32,
					time_to_recover_s: null,
					retry_after_respected: null,
				},
			},
		},
	],
} as unknown as Report;

describe("ResilienceCard dropped faults", () => {
	it("lists every child's faults that did not run", () => {
		fx.evaluation = {
			id: "ev_d10",
			children: [
				{
					benchmark: "hd-harbor-bfcl",
					experiment_id: "e1",
					status: "done",
					faults_skipped: [
						{ fault: "broken-tool", reason: "the benchmark has no tools" },
						{ fault: "misleading-instruction", reason: "the benchmark has no instructions file" },
					],
				},
				{ benchmark: "b2", experiment_id: "e2", status: "done" },
			],
		};
		const html = inStore(<ResilienceCard report={report} />);
		expect(fx.asked).toContain("ev_d10");
		expect(html).toContain('data-testid="report-dropped-faults"');
		expect(html).toContain("Faults that did not run");
		expect(html).toContain("Broken tool on hd-harbor-bfcl: the benchmark has no tools");
		expect(html).toContain("Misleading instruction on hd-harbor-bfcl: the benchmark has no instructions file");
		expect(html).toContain('data-testid="resilience-model-down-A"');
	});

	it("shows nothing extra when every fault ran", () => {
		fx.evaluation = { id: "ev_d10", children: [{ benchmark: "b", experiment_id: "e", status: "done" }] };
		const html = inStore(<ResilienceCard report={report} />);
		expect(html).not.toContain("Faults that did not run");
		expect(html).toContain('data-testid="resilience-model-down-A"');
	});
});

describe("ReportCard dropped faults", () => {
	it("lists the faults that did not run even with no resilience rows", () => {
		fx.evaluation = {
			id: scoreReport.evaluation,
			children: [
				{
					benchmark: "hd-harbor-bfcl",
					experiment_id: "e1",
					status: "done",
					faults_skipped: [{ fault: "broken-tool", reason: "the benchmark has no tools" }],
				},
			],
		};
		const html = inStore(<ReportCard report={{ ...scoreReport, resilience: [] }} readOnly />);
		expect(html).not.toContain(">Resilience<");
		expect(html).toContain("Broken tool on hd-harbor-bfcl: the benchmark has no tools");
	});
});
describe("ResilienceCard range (N58)", () => {
	const noRange = (changed: number | undefined) =>
		({
			...report,
			resilience: [
				{
					fault: "model-down",
					arms: {
						A: {
							tasks: 9,
							degradation_ratio: 1,
							degradation_ratio_low: null,
							degradation_ratio_high: null,
							recovery_rate: 1,
							cost_inflation: null,
							time_to_recover_s: null,
							retry_after_respected: null,
							...(changed === undefined ? {} : { changed_tasks: changed }),
						},
					},
				},
			],
		}) as unknown as Report;

	it("says why there is no range when no task changed", () => {
		fx.evaluation = { id: "ev_d10", children: [] };
		const html = inStore(<ResilienceCard report={noRange(0)} />);
		expect(html).toContain("the fault changed no task&#x27;s result in 9 tasks, too few runs for a range");
		expect(html).not.toContain("x1.00 to x1.00");
	});

	it("says nothing for a missing range of an older report", () => {
		fx.evaluation = { id: "ev_d10", children: [] };
		expect(inStore(<ResilienceCard report={noRange(undefined)} />)).not.toContain("too few runs for a range");
		expect(inStore(<ResilienceCard report={report} />)).toContain("(x0.00 to x1.00)");
	});
});