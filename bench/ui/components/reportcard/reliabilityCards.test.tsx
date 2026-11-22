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