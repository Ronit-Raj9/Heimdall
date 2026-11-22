import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { ModelSwitchExport, ModelSwitchReport } from "@/lib/store/apis/modelSwitchApi";
import { ModelSwitchReportView } from "./modelSwitchReport";

const v = (x: number | null) => (x === null ? { state: "unknown" as const, v: null } : { state: "observed" as const, v: x });

const model = (label: string, q: number, cost: number | null) => ({
	label,
	model: label,
	provider: "openai",
	runs: 10,
	tasks: 10,
	quality: { ...v(q), bounds: [q - 0.1, q + 0.1] as [number, number] },
	cost_per_run: v(cost),
	cost_per_success: v(cost),
	cost_total: v(cost),
	latency_p50_ms: v(800),
	latency_p95_ms: v(1500),
	wall_p50_s: v(3),
	availability: v(1),
	model_calls: 20,
	call_failures: 0,
	fallbacks: 0,
	frontier: true,
	latency_frontier: true,
});

const REPORT = {
	evaluation: "ev1",
	status: "done",
	agent: "support-bot",
	current: "gpt-a",
	alpha: 0.05,
	best: "gpt-b",
	models: [model("gpt-a", 0.6, 0.01), model("gpt-b", 0.8, null)],
	comparisons: [{ label: "gpt-a", against: "gpt-b", tasks: 10, mean_diff: -0.2, diff_low: -0.4, diff_high: 0, p: 0.1, p_adjusted: 0.1, result: "cannot_tell", reason: "Cannot tell apart at 10 tasks." }],
	pareto: { x: "cost_per_run", x_label: "Cost per run", y: "success", points: [], frontier: [], note: "" },
	latency_pareto: { points: [], frontier: [], note: "" },
	benchmarks: [],
	segments: [],
	recommendation: { action: "not_enough_evidence", model: "gpt-b", strength: "weak", summary: "gpt-b leads but the gap is not proven.", reasons: ["higher quality"], caveats: [], tied: [] },
	missing: ["Cost of gpt-b: no priced run."],
	notes: [],
} as unknown as ModelSwitchReport;

const EXPORT: ModelSwitchExport = {
	evaluation: "ev1",
	ready: true,
	config: { $schema: "https://www.getbifrost.ai/schema", governance: { routing_rules: [{ name: "heimdall-ev1-default", enabled: false, cel_expression: "true", targets: [{ provider: "openai", model: "gpt-b", weight: 1 }], fallbacks: ["openai/gpt-a"], scope: "global", priority: 100 }] } },
	rules: [{ name: "heimdall-ev1-default", kind: "default", enabled: false, why: "gpt-b has the best measured quality.", evidence: "not_significant" }],
	headers: [],
	budget_hints: [],
	options: {},
	notes: [],
};

vi.mock("@/lib/store/apis/modelSwitchApi", () => ({
	useGetModelSwitchQuery: () => ({ data: REPORT, isLoading: false, error: undefined, refetch: () => {} }),
	useGetModelSwitchConfigQuery: () => ({ data: EXPORT, isLoading: false, error: undefined, refetch: () => {} }),
	useApplyModelSwitchMutation: () => [async () => ({}), { isLoading: false }],
}));

describe("ModelSwitchReportView", () => {
	const html = renderToStaticMarkup(<ModelSwitchReportView evaluationId="ev1" />);

	it("leads with the recommendation in plain words", () => {
		expect(html).toContain("Not enough evidence");
		expect(html).toContain("gpt-b leads but the gap is not proven.");
	});

	it("shows unknown cost as unknown, never zero, and lists what no run measured", () => {
		expect(html).toContain('data-testid="model-switch-row-gpt-b"');
		expect(html).toContain("unknown");
		expect(html).toContain("Cost of gpt-b: no priced run.");
	});

	it("renders the Bifrost routing config with its download", () => {
		expect(html).toContain("heimdall-ev1-default");
		expect(html).toContain("routing_rules");
		expect(html).toContain('data-testid="model-switch-download"');
	});
});
