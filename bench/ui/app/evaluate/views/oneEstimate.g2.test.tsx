import { render } from "@/lib/testing/render";
import type { EstimatePreset, EvaluationEstimate, EvaluationSpec } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it } from "vitest";
import { budgetLine } from "./budgetAdvice";
import { PresetPicker } from "./presetPicker";
import { defaultEvaluationSpec, estimateText } from "./step4Setup";

// The draft seen live: codex on gpt-6-luna, SWE-bench Verified, 10 tasks once each, budget $0.30.
const spec: EvaluationSpec = {
	...defaultEvaluationSpec(),
	arms: [{ label: "A", agent: "codex", model: "gpt-6-luna" }],
	benchmarks: ["hd-swe-verified"],
	tasks_per_benchmark: 10,
	trials: 1,
	faults: ["clean"],
	budget_usd: 0.3,
};

// The draft's estimate, as POST /evaluations/estimate answers it (step 4's main request and step 5's).
const estimate = {
	runs: 10,
	est_cost_usd: { low: 0.04, high: 0.1, known: true },
	est_minutes: { low: 10, high: 20, known: true },
	budget: {
		budget_usd: 0.3,
		per_benchmark_usd: 0.3,
		default: false,
		default_per_benchmark_usd: 5,
		suggested_usd: 0.3,
		max_usd: 100,
		fit: "fits",
		runs_skipped_low: 0,
		runs_skipped_high: 0,
	},
} as unknown as EvaluationEstimate;

// The presets answer can be older (another request, cached apart): its Quick look range differs.
const quick = {
	id: "quick",
	label: "Quick look",
	summary: "",
	tasks_per_benchmark: 10,
	trials: 1,
	faults: ["clean"],
	runs: 10,
	est_cost_usd: { low: 0.36, high: 2.36, known: true },
	est_minutes: { low: 10, high: 20, known: true },
	mde_pp: 30,
} as unknown as EstimatePreset;

describe("one estimate for one draft", () => {
	it("shows the same low and high on the Quick look card, in step 4 and in step 5", () => {
		const card = render(<PresetPicker spec={spec} presets={[quick]} onPick={() => undefined} draft={estimate} />);
		const step4 = estimateText(estimate).cost;
		const step5 = budgetLine(estimate.budget!, estimate.est_cost_usd, false);
		expect(step4).toBe("$0.04 to $0.10");
		expect(card).toContain("$0.04 to $0.10");
		expect(card).not.toContain("$2.36");
		expect(step5).toContain("$0.04 to $0.10");
	});

	it("keeps a preset's own numbers when it is not the draft's size", () => {
		const card = render(
			<PresetPicker spec={{ ...spec, tasks_per_benchmark: 30 }} presets={[quick]} onPick={() => undefined} draft={estimate} />,
		);
		expect(card).toContain("$0.36 to $2.36");
	});
});