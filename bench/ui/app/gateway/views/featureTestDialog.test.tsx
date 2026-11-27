import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { Experiment, ExperimentPlan } from "@/lib/types/bench";
import type { GwDraftResponse } from "@/lib/types/gateway";
import en from "@/locales/en/uf7c.json";
import { describe, expect, it } from "vitest";
import {
	FEATURE_OPTIONS,
	NEEDS_MODEL,
	PreparedView,
	agentLabel,
	budgetChoices,
	draftRequest,
	missingFields,
	pickableSuites,
	suiteLabel,
	prepareFeatureTest,
	suiteOption,
	startFeatureTest,
	type FeatureTestCalls,
	type FeatureTestForm,
} from "./featureTestDialog";

const form: FeatureTestForm = {
	suite: "swe-lite@1.0.0",
	base: "agents/codex.yaml",
	model: "gpt-6",
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
			suite: "swe-lite@1.0.0",
			base: "agents/codex.yaml",
			model: "gpt-6",
			trials: 2,
			budget_usd: 5,
			options: { grid: "0.80,0.90" },
		});
		expect(draftRequest("complexity_router", { ...form, options: {} }).problem).toBe("needFields");
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

	it("shows the notes, the estimate, plan refusals and the confirmation with estimate and cap", () => {
		const html = render(<PreparedView prepared={{ draft: drafted, plan }} budget={5} confirming />);
		expect(html).toContain("Answers are judged by your benchmark&#x27;s own grader.");
		expect(html).toContain("About $3.50 and about 10 minutes. Budget is the most it can spend.");
		expect(html).toContain("Start this test? About $3.50, at most $5.00.");
		const refused = render(
			<PreparedView prepared={{ draft: drafted, plan: { ...plan, errors: ["identical setups"] } }} budget={5} confirming={false} />,
		);
		expect(refused).toContain("This test cannot start yet:");
		expect(refused).toContain("identical setups");
	});

	it("gives every setting of every feature a label and a hint, never a raw key", () => {
		const opt = en.dialog.opt as Record<string, { label: string; hint: string }>;
		for (const [feature, opts] of Object.entries(FEATURE_OPTIONS)) {
			for (const o of opts) {
				expect(opt[o.key]?.label, `${feature}.${o.key}`).toBeTruthy();
				expect(opt[o.key].hint, `${feature}.${o.key}`).toBeTruthy();
				expect(opt[o.key].label).not.toMatch(/_/);
			}
		}
	});

	it("needs a model for the semantic cache and not the skill today (N7-1, N7-2)", () => {
		expect(NEEDS_MODEL.has("semantic_cache")).toBe(true);
		expect(missingFields("semantic_cache", { ...form, model: "" })).toEqual(["model"]);
		expect(draftRequest("semantic_cache", { ...form, model: "" }).problem).toBe("needModel");
		expect(draftRequest("semantic_cache", form).req?.model).toBe("gpt-6");
		expect(FEATURE_OPTIONS.skill_version.find((o) => o.key === "control_path")?.required).toBe(false);
		expect(draftRequest("skill_version", { ...form, options: { variant_path: "agents/codex/skills/x" } }).req).toBeTruthy();
	});

	it("refuses trials below 1 and marks the missing fields", () => {
		expect(draftRequest("gateway_overhead", { ...form, trials: "0" }).problem).toBe("needTrials");
		expect(missingFields("providers", { ...form, suite: "", base: "" })).toEqual(["suite", "base", "targets"]);
		expect(draftRequest("gateway_overhead", form).req).toBeTruthy();
	});

	it("shows names in the selects and hides test benchmarks", () => {
		const u = (k: string, o: Record<string, unknown>) => `${k}:${JSON.stringify(o)}`;
		expect(suiteLabel(u, { id: "swe-lite", task_count: 120 })).toBe('dialog.suiteOption:{"name":"swe-lite","count":120}');
		expect(agentLabel(u, { id: "codex", model: "gpt-6" })).toBe('dialog.agentOption:{"name":"codex","model":"gpt-6"}');
		expect(agentLabel(u, { id: "codex", model: "" })).toBe("codex");
		expect(pickableSuites([{ id: "zz-test-a" }, { id: "swe-lite" }])).toEqual([{ id: "swe-lite" }]);
		expect(budgetChoices("0.5, 1, x, 1, 2")).toEqual(["0.5", "1", "2"]);
	});

	it("sends the suite as id@version and the agent as its file path, as the server reads them", () => {
		expect(suiteOption({ id: "swe-lite", version: "1.0.0" })).toBe("swe-lite@1.0.0");
		expect(suiteOption({ id: "swe-lite" })).toBe("swe-lite");
	});
});