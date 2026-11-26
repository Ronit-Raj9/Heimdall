import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { Experiment, ExperimentPlan } from "@/lib/types/bench";
import type { GwDraftResponse } from "@/lib/types/gateway";
import { describe, expect, it } from "vitest";
import {
	PreparedView,
	draftRequest,
	prepareFeatureTest,
	startFeatureTest,
	type FeatureTestCalls,
	type FeatureTestForm,
} from "./featureTestDialog";

const form: FeatureTestForm = {
	suite: "swe-lite",
	base: "codex",
	model: "",
	provider: "",
	budget: "5",
	trials: "2",
	options: { grid: "0.80,0.90" },
};
const exp = { name: "Bifrost test: Semantic cache" } as unknown as Experiment;
const plan = {
	errors: [],
	warnings: [],
	est_usd: { state: "observed", v: 3.5 },
	est_wall_s: { state: "observed", v: 600 },
} as unknown as ExperimentPlan;
const drafted: GwDraftResponse = { experiment: exp, notes: ["Answers are judged by your benchmark's own grader."] };

function fakeCalls(log: string[]): FeatureTestCalls {
	return {
		draft: async (req) => (log.push(`draft:${req.feature}:${req.budget_usd}`), drafted),
		plan: async (e) => (log.push(`plan:${e.name}`), plan),
		create: async (e) => (log.push(`create:${e.name}`), { exp_id: "e9" }),
		start: async (id) => (log.push(`start:${id}`), {}),
	};
}

describe("Feature test dialog", () => {
	it("builds the draft request and refuses a missing budget or required setting", () => {
		expect(draftRequest("semantic_cache", form).req).toEqual({
			feature: "semantic_cache",
			suite: "swe-lite",
			base: "codex",
			trials: 2,
			budget_usd: 5,
			options: { grid: "0.80,0.90" },
		});
		expect(draftRequest("semantic_cache", { ...form, budget: "" }).problem).toBe("needBudget");
		expect(draftRequest("semantic_cache", { ...form, budget: "51" }).problem).toBe("needBudget");
		expect(draftRequest("providers", form).problem).toBe("needFields");
		expect(draftRequest("semantic_cache", { ...form, base: "" }).problem).toBe("needFields");
	});

	it("runs draft, plan, then create and start in order", async () => {
		const log: string[] = [];
		const calls = fakeCalls(log);
		const prepared = await prepareFeatureTest(calls, draftRequest("semantic_cache", form).req!);
		expect(log).toEqual(["draft:semantic_cache:5", "plan:Bifrost test: Semantic cache"]);
		expect(await startFeatureTest(calls, prepared.draft.experiment)).toBe("e9");
		expect(log.slice(2)).toEqual(["create:Bifrost test: Semantic cache", "start:e9"]);
	});

	it("stops at the draft's refusal and starts nothing", async () => {
		const log: string[] = [];
		const calls = { ...fakeCalls(log), draft: () => Promise.reject(new Error("refused")) };
		await expect(prepareFeatureTest(calls, draftRequest("semantic_cache", form).req!)).rejects.toThrow("refused");
		expect(log).toEqual([]);
	});

	it("shows the notes, the estimate, plan refusals and the confirmation", () => {
		const html = render(<PreparedView prepared={{ draft: drafted, plan }} budget={5} confirming />);
		expect(html).toContain("Answers are judged by your benchmark&#x27;s own grader.");
		expect(html).toContain("Estimate: about 3.50 USD and 10 minutes.");
		expect(html).toContain("Start this test? It can spend up to 5.00 USD.");
		const refused = render(
			<PreparedView prepared={{ draft: drafted, plan: { ...plan, errors: ["identical setups"] } }} budget={5} confirming={false} />,
		);
		expect(refused).toContain("This test cannot start yet:");
		expect(refused).toContain("identical setups");
	});
});