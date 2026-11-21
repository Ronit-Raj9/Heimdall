/** Improvement runs (bench/improve, GET /improvements). Mirrors improve.Summary, Detail and friends. */

export type ImprovementStatus = "planned" | "running" | "awaiting_approval" | "done" | "stopped" | "failed";
export type ImprovementVerdictState = "supported" | "not_supported" | "inconclusive" | "no_change";
export type LineageStatus = "base" | "committed" | "rejected" | "tripped" | "control" | "pending";

export interface ImprovementSummary {
	id: string;
	name: string;
	agent: string;
	suite: string;
	status: ImprovementStatus;
	reason: string;
	running: boolean;
	created_at: string;
	updated_at: string;
	created_by: string;
	rounds: number;
	rounds_done: number;
	commits: number;
	incumbent: string;
	verdict?: ImprovementVerdictState;
	sealed_gain?: number;
	total_usd: number;
}

export interface ImprovementList {
	improvements: ImprovementSummary[];
}

export interface CurvePoint {
	round: number;
	dev?: number;
	audit?: number;
	sealed?: number;
	cand_dev?: number;
	cand_audit?: number;
	event: LineageStatus;
}

export interface ControlBand {
	low: number;
	high: number;
	mean: number;
	best_of_k: number;
	trials: number;
	loop_usd: number;
	null_mean?: number;
	null_ref?: string;
}

export interface ScoreView {
	mean: number;
	runs: number;
	known: number;
	vetoed: number;
}

export interface EValueTest {
	alpha: number;
	threshold: number;
	min_pairs: number;
	max_pairs: number;
	pairs: number;
	e_up: number;
	e_down: number;
	max_e_up: number;
	path: number[];
	better: boolean;
	worse: boolean;
	crossed_at: number;
}

export interface CompareView {
	exp_id: string;
	a: ScoreView;
	b: ScoreView;
	mean_diff: number;
	pairs: number;
	p?: number;
	e_value?: EValueTest;
	greedy: boolean;
	honeypot_passes: number;
}

export interface DiffLine {
	op: "+" | "-" | " ";
	text: string;
}

export interface FileDiff {
	path: string;
	status: "added" | "removed" | "changed";
	lines: DiffLine[];
	added: number;
	removed: number;
}

export interface GateResult {
	name: string;
	fired: boolean;
	detail: string;
	evidence?: string[];
}

export interface Approval {
	stage: string;
	by: string;
	reason: string;
	at: string;
}

export interface RoundCost {
	improver: number;
	screen: number;
	audit: number;
	sealed: number;
}

export interface RoundView {
	n: number;
	stage: string;
	decision: "" | "committed" | "rejected";
	why: string;
	parent: string;
	candidate?: string;
	improver?: { cost_usd: number; run_id?: string; exp_id?: string; flags?: string[] };
	diff: FileDiff[];
	gates: GateResult[];
	tripwires: string[];
	approvals: Approval[];
	screen?: CompareView;
	audit?: CompareView;
	sealed?: ScoreView;
	cost: RoundCost;
	started_at: string;
	finished_at?: string;
}

export interface LineageNode {
	seq: number;
	round: number;
	ref: string;
	version: number;
	parent?: string;
	author: "human" | "agent";
	bundle_hash: string;
	config_hash: string;
	status: LineageStatus;
	reason: string;
	produced_by?: string;
	at: string;
}

export interface LineageView {
	nodes: LineageNode[];
	entries: number;
	chain_ok: boolean;
	chain_error?: string;
}

export interface IntegrityRow {
	round: number;
	fired: string[];
	tripwires: string[];
	overridden: boolean;
	vetoes_incumbent: number;
	vetoes_candidate: number;
	honeypot_passes: number;
}

export interface CostView {
	baseline: number;
	rounds: RoundCost[];
	controls: number;
	total: number;
	improver_cap_per_round: number;
}

export interface ImprovementVerdict {
	state: ImprovementVerdictState;
	headline: string;
	sealed_gain: number;
	p?: number;
	dev_gain: number;
	generalization_gap: number;
	commits: number;
	rejects: number;
	tripwires: number;
	overrides: number;
	greedy_extra: number;
	total_usd: number;
	gain_per_usd?: number;
	notes: string[];
}

export interface ImprovementDetail extends Omit<ImprovementSummary, "verdict"> {
	verdict?: ImprovementVerdictState;
	spec_hash: string;
	spec: { rounds: number; editable: { files: string[]; dirs?: string[] }; improver: { budget_usd: number }; commit: { alpha: number } };
	splits: { dev: string[]; audit: number; sealed: number; honeypots: number; hash: string };
	base: { ref: string; number: number; config_hash: string; bundle_hash: string };
	curve: CurvePoint[];
	control_band?: ControlBand;
	round_views: RoundView[];
	lineage: LineageView;
	integrity: IntegrityRow[];
	cost: CostView;
	verdict_detail?: ImprovementVerdict;
	error?: string;
	audit_looks: number;
	audit_looks_max: number;
}
