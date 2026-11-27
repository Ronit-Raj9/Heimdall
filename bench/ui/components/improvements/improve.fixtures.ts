import type {
	ImprovementDetail,
	ImprovementEstimate,
	ImprovementVerdict,
	ImprovementVerdictState,
	VersionExplain,
} from "@/lib/types/improve";

/** A finished two-round run (round 1 kept, round 2 paused then rejected), for tests only. */
export const IMPROVEMENT_FIXTURE: ImprovementDetail = {
	id: "imp_notes-loop-abc123",
	name: "notes-loop",
	agent: "agents/coder.yaml",
	suite: "coding-core@1.0.0",
	status: "done",
	reason: "improvement supported: +20.0 points on the held-out tasks, above both comparisons",
	running: false,
	created_at: "2026-11-22T10:00:00Z",
	updated_at: "2026-11-22T11:00:00Z",
	created_by: "ana",
	rounds: 2,
	rounds_done: 2,
	commits: 1,
	incumbent: "agents/coder.yaml@v2",
	verdict: "supported",
	sealed_gain: 0.2,
	total_usd: 1.234,
	spec_hash: "h",
	spec: { rounds: 2, editable: { files: ["/home/agent/AGENTS.md"] }, improver: { budget_usd: 2 }, commit: { alpha: 0.05 } },
	splits: { dev: ["t1", "t2"], audit: 6, sealed: 6, honeypots: 0, hash: "s" },
	base: { ref: "agents/coder.yaml@v1", number: 1, config_hash: "c1", bundle_hash: "b1" },
	curve: [
		{ round: 0, dev: 0.2, sealed: 0.3, event: "base" },
		{ round: 1, dev: 0.6, audit: 0.55, sealed: 0.5, cand_dev: 0.6, cand_audit: 0.55, event: "committed" },
		{ round: 2, dev: 0.6, audit: 0.55, sealed: 0.5, cand_dev: 0.6, cand_audit: 0.5, event: "rejected" },
	],
	control_band: {
		low: 0.25,
		high: 0.35,
		mean: 0.3,
		best_of_k: 0.4,
		trials: 3,
		loop_usd: 1,
		null_mean: 0.3,
		null_ref: "agents/coder.yaml@v4",
	},
	round_views: [
		{
			n: 1,
			stage: "done",
			decision: "committed",
			why: "better on the gate tasks: evidence reached 40 after 18 pairs",
			parent: "agents/coder.yaml@v1",
			candidate: "agents/coder.yaml@v2",
			improver: { cost_usd: 0.1, run_id: "r_1" },
			diff: [
				{
					path: "/home/agent/AGENTS.md",
					status: "changed",
					lines: [
						{ op: "-", text: "level: 0" },
						{ op: "+", text: "level: 3" },
					],
					added: 1,
					removed: 1,
				},
			],
			gates: [{ name: "improver_answer_key", fired: false, detail: "ok" }],
			tripwires: [],
			approvals: [],
			screen: {
				exp_id: "e1",
				a: { mean: 0.2, runs: 4, known: 4, vetoed: 0 },
				b: { mean: 0.6, runs: 4, known: 4, vetoed: 0 },
				mean_diff: 0.4,
				pairs: 4,
				p: 0.1,
				greedy: false,
				honeypot_passes: 0,
			},
			audit: {
				exp_id: "e2",
				a: { mean: 0.2, runs: 30, known: 30, vetoed: 0 },
				b: { mean: 0.55, runs: 30, known: 30, vetoed: 0 },
				mean_diff: 0.35,
				pairs: 18,
				greedy: true,
				honeypot_passes: 0,
				e_value: {
					alpha: 0.025,
					threshold: 40,
					min_pairs: 4,
					max_pairs: 60,
					pairs: 18,
					e_up: 41,
					e_down: 0.1,
					max_e_up: 41,
					path: [1, 2, 41],
					better: true,
					worse: false,
					crossed_at: 18,
				},
			},
			sealed: { mean: 0.5, runs: 12, known: 12, vetoed: 0 },
			cost: { improver: 0.1, screen: 0.1, audit: 0.3, sealed: 0.1 },
			started_at: "2026-11-22T10:01:00Z",
		},
		{
			n: 2,
			stage: "done",
			decision: "rejected",
			why: "not shown better on the gate tasks: evidence 1.20, 40 needed, after 30 pairs (a paused check was approved)",
			parent: "agents/coder.yaml@v2",
			candidate: "agents/coder.yaml@v3",
			diff: [],
			gates: [{ name: "improver_monitor_parity", fired: true, detail: "stopped", evidence: ["event:tool_call"] }],
			tripwires: ["improver_monitor_parity"],
			approvals: [{ stage: "screen", by: "bo", reason: "logging change reviewed", at: "2026-11-22T10:30:00Z" }],
			sealed: { mean: 0.5, runs: 12, known: 12, vetoed: 0 },
			cost: { improver: 0.1, screen: 0.1, audit: 0.3, sealed: 0.1 },
			started_at: "2026-11-22T10:20:00Z",
		},
	],
	lineage: {
		nodes: [
			{
				seq: 1,
				round: 0,
				ref: "agents/coder.yaml@v1",
				version: 1,
				author: "human",
				bundle_hash: "b1",
				config_hash: "c1",
				status: "base",
				reason: "version 0",
				at: "2026-11-22T10:00:00Z",
			},
			{
				seq: 2,
				round: 1,
				ref: "agents/coder.yaml@v2",
				version: 2,
				parent: "agents/coder.yaml@v1",
				author: "agent",
				bundle_hash: "b2",
				config_hash: "c2",
				status: "committed",
				reason: "better",
				at: "2026-11-22T10:05:00Z",
			},
			{
				seq: 4,
				round: 2,
				ref: "agents/coder.yaml@v3",
				version: 3,
				parent: "agents/coder.yaml@v2",
				author: "agent",
				bundle_hash: "b3",
				config_hash: "c3",
				status: "rejected",
				reason: "not shown better",
				at: "2026-11-22T10:25:00Z",
			},
		],
		entries: 5,
		chain_ok: true,
	},
	integrity: [
		{ round: 1, fired: [], tripwires: [], overridden: false, vetoes_incumbent: 0, vetoes_candidate: 0, honeypot_passes: 0 },
		{
			round: 2,
			fired: ["improver_monitor_parity"],
			tripwires: ["improver_monitor_parity"],
			overridden: true,
			vetoes_incumbent: 0,
			vetoes_candidate: 0,
			honeypot_passes: 0,
		},
	],
	cost: {
		baseline: 0.1,
		rounds: [
			{ improver: 0.1, screen: 0.1, audit: 0.3, sealed: 0.1 },
			{ improver: 0.1, screen: 0.1, audit: 0.3, sealed: 0.1 },
		],
		controls: 0.034,
		total: 1.234,
		improver_cap_per_round: 2,
	},
	verdict_detail: {
		state: "supported",
		headline: "improvement supported: +20.0 points on the held-out tasks, above both comparisons",
		sealed_gain: 0.2,
		p: 0.03,
		dev_gain: 0.4,
		generalization_gap: 0.2,
		commits: 1,
		rejects: 1,
		tripwires: 1,
		overrides: 1,
		greedy_extra: 0,
		total_usd: 1.234,
		notes: ["a single loop: rerun the improvement from version 0 to show the gain holds across loops"],
	},
	audit_looks: 2,
	audit_looks_max: 2,
};

/** The headline each verdict state of this wave reads (A2.12), with numbers filled in. */
export const HEADLINES: Record<string, string> = {
	supported: "Version 2 is better: +20 points on 18 held-out tasks it never saw (95% interval 8.0 to 32 points, p 0.012).",
	likely_better:
		"Likely better, not proven: +6.0 points on 18 held-out tasks (80% interval 0.5 to 12 points, p 0.11). This many tasks can confirm gains of about 24 points or more.",
	insufficient_sample:
		"Not enough held-out tasks to tell: 6 tasks, 2 of them with different results for the two versions. Even a clear gain could not be confirmed with this many. Observed: +4.0 points.",
	no_evidence:
		"No clear change on the held-out tasks: +1.0 points on 18 tasks (80% interval -4.0 to 6.0 points). Keep version 0 unless you have other reasons.",
	worse: "Version 2 is worse: -15 points on 18 held-out tasks (95% interval -26 to -4.0 points, p 0.008). Keep version 0.",
	no_change: "No new version was kept: none passed the gate. The agent stays at version 0.",
	not_claimable:
		"A person approved a paused check in round 2, so this result cannot be claimed. Observed: +3.0 points on 18 held-out tasks.",
};

/** A verdict of state with its headline and, for the states that offer one, a continue offer. */
export function verdictFixture(state: ImprovementVerdictState): ImprovementVerdict {
	const offer = state === "likely_better" || state === "insufficient_sample";
	return {
		...IMPROVEMENT_FIXTURE.verdict_detail!,
		state,
		headline: HEADLINES[state],
		gain: 0.06,
		ci80: [0.005, 0.12],
		ci95: [-0.02, 0.14],
		p_upper: 0.11,
		n: 18,
		n_nonzero: 9,
		mde: 0.24,
		sentences: [HEADLINES[state], "One run of the loop. Run it twice more from version 0 to see whether the gain repeats."],
		continue: offer
			? {
					extra_trials: 2,
					new_tasks: 12,
					runs: 96,
					usd: [3.1, 6.4],
					can_confirm: 9,
					text: "To confirm a 5 point gain: about 30 more held-out tasks, or 4 more tries on each task, about $6.40.",
				}
			: undefined,
	};
}

/** The run in state, finished, with can_continue set for the states that offer a second look. */
export function stateFixture(state: ImprovementVerdictState): ImprovementDetail {
	const v = verdictFixture(state);
	return {
		...IMPROVEMENT_FIXTURE,
		verdict: state,
		verdict_detail: v,
		can_continue: v.continue !== undefined,
		can_resume: false,
		limit_usd: 15,
		spent_usd: 1.234,
	};
}

/** An estimate for a 60 task benchmark, as A2.8 shows it. */
export const ESTIMATE_FIXTURE: ImprovementEstimate = {
	ok: true,
	pool: 60,
	splits: { practice: 24, gate: 18, held_out: 18, impossible: 0 },
	runs: { baseline: 24, improver: 9, practice: 144, gate: 216, held_out_curve: 0, final: 108, total: 492 },
	usd: { low: 15.8, expected: 22.6, high: 33.9, limit: 45, per_run: 0.03, per_run_source: "forecast" },
	minutes: { low: 80, high: 160 },
	detectable: { gate_points: 23.5, held_out_points: 23.9 },
	assumed: ["pass rate 50%"],
	refusals: [],
	warnings: [
		{
			code: "weak_held_out",
			message:
				"With 18 held-out tasks only a gain of about 24 points or more can be confirmed. A smaller real gain will end as not proven yet.",
		},
	],
};

/** What version 2 changed against version 1. */
export const EXPLAIN_FIXTURE: VersionExplain = {
	ref: "agents/coder.yaml@v2",
	parent_ref: "agents/coder.yaml@v1",
	round: 1,
	source: "mutation",
	improver: "heimdall-improver@1.1.0:reflect-edit",
	model: "gpt-5-mini",
	files: [
		{
			path: "/home/agent/AGENTS.md",
			status: "changed",
			lines: [
				{ op: "-", text: "level: 0" },
				{ op: "+", text: "level: 3" },
			],
			edits: [
				{
					op: "add_item",
					id: "pb-0001",
					reason: "Runs stopped before the tests ran",
					evidence_count: 3,
					predicts_fixed: 2,
					result: "applied",
				},
			],
		},
	],
	rationale: "Most failures ended before the tests ran.",
	gates: [{ name: "improver_answer_key", fired: false, detail: "ok" }],
	decision: "committed",
	why: "Kept: better on 18 gate tasks by 20 points (p 0.01, needed 0.033).",
};