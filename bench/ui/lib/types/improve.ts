/** Improvement runs (bench/improve, GET /improvements). Mirrors improve.Summary, Detail and friends. */

export type ImprovementStatus = "planned" | "running" | "awaiting_approval" | "done" | "stopped" | "failed" | "interrupted";
/** Verdict states (improve/record.go). not_supported and inconclusive are states of older runs. */
export type ImprovementVerdictState =
	| "supported"
	| "likely_better"
	| "insufficient_sample"
	| "no_evidence"
	| "worse"
	| "no_change"
	| "not_claimable"
	| "not_supported"
	| "inconclusive";
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
	/** Set when the improver ran on a teacher model (A3.7). */
	teacher?: string;
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
	/** Served for the practice comparison only (A1.10). */
	exp_id?: string;
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
	/** How by reads (a member's display name). */
	by_name?: string;
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
	candidates?: CandidateView[];
	commit?: CommitView;
	winner?: string;
	stages?: StageView[];
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
	/** The claim's numbers (A2.5). Absent on older runs. */
	gain?: number;
	ci80?: [number, number];
	ci95?: [number, number];
	p_upper?: number;
	p_lower?: number;
	n?: number;
	n_nonzero?: number;
	/** Smallest gain the held-out tasks can confirm, as a fraction; 0 when there were fewer than 2 tasks. */
	mde?: number;
	level?: number;
	teacher?: string;
	null?: ControlLine;
	retries?: ControlLine;
	continue?: ContinueOffer;
	/** The final wording (A2.12): the headline first, then the lines under it. */
	sentences?: string[];
}

export interface ControlLine {
	score?: number;
	diff?: number;
	ci80?: [number, number];
	note?: string;
}

/** What a second look on more tasks or tries would cost and could confirm (A2.11). */
export interface ContinueOffer {
	extra_trials?: number;
	new_tasks?: number;
	runs?: number;
	usd?: [number, number];
	/** Points. */
	can_confirm?: number;
	text?: string;
}

/** The body of POST .../continue/estimate and, with extra_budget_usd, of POST .../continue. */
export interface ContinueRequest {
	extra_trials: number;
	new_tasks: boolean;
	extra_budget_usd?: number;
}

export interface EstimateNote {
	code: string;
	message: string;
}

/** The pre-run estimate (A2.8), answered by POST /improvements/estimate. */
export interface ImprovementEstimate {
	ok: boolean;
	pool: number;
	splits: { practice: number; gate: number; held_out: number; impossible: number };
	runs: { baseline: number; improver: number; practice: number; gate: number; held_out_curve: number; final: number; total: number };
	/** A study's limit is the total for all loops: loop_limit is each loop's share, limit_text says both. */
	usd: {
		low: number;
		expected: number;
		high: number;
		limit: number;
		per_run: number;
		per_run_source: string;
		loop_limit?: number;
		limit_text?: string;
	};
	minutes: { low: number; high: number };
	detectable: { gate_points: number; held_out_points: number };
	assumed: string[] | null;
	refusals: EstimateNote[] | null;
	warnings: EstimateNote[] | null;
	/** The number of whole loops of a study; absent for one run. */
	loops?: number;
}

export interface AppliedEditView {
	op: string;
	id?: string;
	reason: string;
	result: string;
	why?: string;
}

/** One idea of a round (A3.4). */
export interface CandidateView {
	id: string;
	slot: number;
	kind: "mutation" | "merge" | "restart" | "null" | string;
	source?: string;
	dropped?: string;
	dev_diff?: number;
	dev_tasks?: number;
	cost_usd?: number;
	edits?: AppliedEditView[];
}

/** The gate decision of a round (A2.2). */
export interface CommitView {
	p: number;
	level: number;
	mean: number;
	n: number;
	n_nonzero: number;
	missing: number;
	skipped: boolean;
	why: string;
}

export interface StageView {
	level: number;
	tasks: number;
	survivors?: string[];
}

export interface KeepView {
	ref: string;
	dev_diff?: number;
	led?: string[];
}

export interface FinalView {
	n: number;
	n_nonzero: number;
	gain: number;
	ci80: [number, number];
	ci95: [number, number];
	p_upper: number;
	trials: number;
}

/** One edit in the explain view, with counts in place of the evidence refs. */
export interface ExplainEdit {
	op: string;
	id?: string;
	reason: string;
	evidence_count: number;
	predicts_fixed: number;
	result: string;
}

export interface ExplainFile {
	path: string;
	status: "added" | "removed" | "changed" | "unchanged";
	lines: DiffLine[];
	edits: ExplainEdit[];
}

/** GET /improvements/{id}/versions/{n}/explain (A4.3): what changed in a version and why. */
export interface VersionExplain {
	ref: string;
	parent_ref: string;
	round: number;
	source: string;
	improver: string;
	model: string;
	files: ExplainFile[];
	rationale: string;
	gates: GateResult[];
	decision: string;
	why: string;
}

export interface ImprovementDetail extends Omit<ImprovementSummary, "verdict"> {
	verdict?: ImprovementVerdictState;
	spec_hash: string;
	spec: {
		rounds: number;
		editable: { files: string[]; dirs?: string[]; instructions?: boolean };
		improver: { budget_usd: number; candidates?: number; model?: string; task?: string };
		commit: { alpha: number };
		preset?: string;
		sealed?: { trials: number; curve?: boolean };
		budget?: { total_usd: number };
	};
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
	/** The WP6 additions; each is absent on a server without them. */
	estimate?: ImprovementEstimate;
	spent_usd?: number;
	limit_usd?: number;
	revision?: number;
	can_resume?: boolean;
	can_continue?: boolean;
	study?: string;
	keep?: KeepView[];
	final?: FinalView;
}