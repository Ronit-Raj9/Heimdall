import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { ModelSwitchReport } from "@/lib/store/apis/modelSwitchApi";
import { ModelSwitchReportView } from "./modelSwitchReport";

const v = (x: number) => ({ state: "observed" as const, v: x });
const model = (label: string) => ({
	label, model: label, provider: "openai", runs: 3, tasks: 3, quality: { ...v(0), bounds: [0, 0] as [number, number] },
	cost_per_run: v(0.0005), cost_per_success: { state: "unknown" as const, v: null }, cost_total: v(0.0015),
	latency_p50_ms: v(2753), latency_p95_ms: v(3000), wall_p50_s: v(3), availability: v(1), model_calls: 3, call_failures: 0, fallbacks: 0,
	frontier: true, latency_frontier: true,
});

// The H4 run of round 3: three models, 0 of 3 solved each; the server answers nothing_solved with no model.
const REPORT = {
	evaluation: "ev_h4", status: "done", agent: "bare-model", current: "A", alpha: 0.05, best: "",
	models: [model("A"), model("B"), model("C")], comparisons: [],
	pareto: { x: "cost_per_run", x_label: "Cost per run", y: "success", points: [], frontier: [], note: "" },
	latency_pareto: { points: [], frontier: [], note: "" }, benchmarks: [], segments: [],
	recommendation: { action: "nothing_solved", model: "", strength: "weak", summary: "No model solved a task. Nothing to recommend.", reasons: [], caveats: [], tied: [] },
	missing: [], notes: [],
} as unknown as ModelSwitchReport;

vi.mock("@/lib/store/apis/modelSwitchApi", () => ({
	useGetModelSwitchQuery: () => ({ data: REPORT, isLoading: false, error: undefined, refetch: () => {} }),
	useGetModelSwitchConfigQuery: () => ({ data: undefined, isLoading: false, error: undefined, refetch: () => {} }),
	useApplyModelSwitchMutation: () => [async () => ({}), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/secretsApi", () => ({ useListSecretsQuery: () => ({ data: { secrets: [] } }) }));

describe("ModelSwitchReportView with nothing solved", () => {
	const html = renderToStaticMarkup(<ModelSwitchReportView evaluationId="ev_h4" />);
	it("recommends no model and calls none best", () => {
		expect(html).toContain("Nothing to recommend");
		expect(html).toContain("No model solved a task. Nothing to recommend.");
		expect(html).not.toContain("recommended");
		expect(html).not.toMatch(/\(current, best/);
		expect(html).not.toContain("Unproven lean");
	});
});
