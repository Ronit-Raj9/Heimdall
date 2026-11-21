import type { Report } from "@/lib/store/apis/evaluationsApi";

const m = (score: number, ci_low: number, ci_high: number, runs = 30) => ({
	score,
	ci_low,
	ci_high,
	runs,
	cost_usd: 3.2,
	tokens: 123456,
	steps: 34,
	tool_calls: 21,
	wall_s: 410,
});

/** One arm: "how good is my agent". */
export const scoreReport: Report = {
	evaluation: "ev_score1",
	arms: [{ label: "A", agent: "codex", version: 2, model: "gpt-6.2-sol" }],
	capabilities: [
		{ capability: "terminal", benchmarks: ["hd-harbor-tb2"], arms: { A: m(0.4, 0.3, 0.52, 30) } },
		{ capability: "coding", benchmarks: ["hd-swe-verified"], arms: { A: m(0.6, 0.45, 0.73, 10) } },
	],
	benchmarks: [
		{ benchmark: "hd-harbor-tb2", capability: "terminal", experiment_id: "exp_tb2", arms: { A: m(0.4, 0.3, 0.52, 30) } },
		{ benchmark: "hd-swe-verified", capability: "coding", experiment_id: "exp_swe", arms: { A: m(0.6, 0.45, 0.73, 10) } },
	],
	reliability: [{ fault: "model-down", arms: { A: { score: 0.2, drop: -0.21 } } }],
	safety: { attack_resisted: { A: 0.88 }, cheat_rate: { A: 0.05 } },
};

/** Two arms: v1 vs v2. B wins 3, loses 1, ties 2. */
export const abReport: Report = {
	evaluation: "ev_ab1",
	arms: [
		{ label: "A", agent: "codex", version: 1, model: "gpt-6.2-sol" },
		{ label: "B", agent: "codex", version: 2, model: "gpt-6.2-sol" },
	],
	capabilities: [
		{ capability: "terminal", benchmarks: ["hd-harbor-tb2"], arms: { A: m(0.3, 0.2, 0.4), B: m(0.6, 0.5, 0.7) } },
		{ capability: "memory", benchmarks: ["longmemeval"], arms: { A: m(0.5, 0.4, 0.6), B: m(0.52, 0.42, 0.62) } },
	],
	benchmarks: [
		{
			benchmark: "hd-harbor-tb2",
			capability: "terminal",
			experiment_id: "exp_1",
			arms: { A: m(0.3, 0.2, 0.4), B: m(0.6, 0.5, 0.7) },
			verdict: { winner: "B", delta: 0.3, significant: true, summary: "B solves 30 points more" },
		},
		{
			benchmark: "b2",
			capability: "coding",
			experiment_id: "exp_2",
			arms: { A: m(0.3, 0.2, 0.4), B: m(0.6, 0.5, 0.7) },
			verdict: { winner: "B", delta: 0.3, significant: true, summary: "" },
		},
		{ benchmark: "b3", capability: "coding", experiment_id: "exp_3", arms: { A: m(0.3, 0.2, 0.4), B: m(0.6, 0.5, 0.7) } },
		{ benchmark: "b4", capability: "support", experiment_id: "exp_4", arms: { A: m(0.7, 0.6, 0.8), B: m(0.4, 0.3, 0.5) } },
		{ benchmark: "longmemeval", capability: "memory", experiment_id: "exp_5", arms: { A: m(0.5, 0.4, 0.6), B: m(0.52, 0.42, 0.62) } },
		{ benchmark: "b6", capability: "research", experiment_id: "exp_6", arms: { A: m(0.5, 0.4, 0.6), B: m(0.5, 0.4, 0.6) } },
	],
	reliability: [{ fault: "broken-tool", arms: { A: { score: 0.1, drop: -0.3 }, B: { score: 0.4, drop: -0.15 } } }],
	safety: { attack_resisted: { A: 0.7, B: 0.9 }, cheat_rate: { A: 0.1, B: 0.02 } },
	alignment: {
		categories: [
			{
				id: "destructive",
				label: "Destructive actions",
				description: "Destroyed user work or took an irreversible action without approval.",
				arms: { A: { rate: 0.25, ci_low: 0.03, ci_high: 0.65, fired: 2, runs: 8 }, B: { rate: 0, ci_low: 0, ci_high: 0.37, fired: 0, runs: 8 } },
			},
			{ id: "injection", label: "Prompt injection", arms: {} },
		],
		impossible_flagged: { B: { rate: 1, ci_low: 0.29, ci_high: 1, fired: 3, runs: 3 } },
		owasp_agentic: [
			{ id: "ASI01", name: "Agent Goal Hijack", tested: false },
			{ id: "ASI02", name: "Tool Misuse and Exploitation", tested: true },
		],
	},
};