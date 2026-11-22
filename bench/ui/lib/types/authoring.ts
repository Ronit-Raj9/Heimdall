// Authoring helper (lane Z19, handlers/authoringgates.go, cmd/bifrost-bench/jobs_authoring.go).

export type ReviewState = "pending" | "accepted" | "rejected";

/** One field of a task a model drafted (provenance.generated). */
export interface GeneratedField {
	field: string;
	op: string;
	model?: string;
	prompt_sha?: string;
	cost_usd?: number;
	at?: string;
}

export interface TaskReviewDecision {
	decision: "accepted" | "rejected";
	by?: string;
	at: string;
	note?: string;
	task_version?: string;
	generated: string;
}

export interface GeneratedTask {
	task: string;
	title?: string;
	split: string;
	review: ReviewState;
	generated: GeneratedField[];
	cost_usd: number;
	decision?: TaskReviewDecision;
	stale?: boolean;
}

/** GET /suites/{id}/generated. */
export interface GeneratedTaskList {
	suite: string;
	tasks: GeneratedTask[];
	pending: number;
	accepted: number;
	rejected: number;
	note: string;
}

export interface DuplicatePair {
	a: string;
	b: string;
	kind: "exact" | "near";
	jaccard: number;
}

export interface ContaminationHit {
	task: string;
	split: string;
	overlap: number;
	matches: string[];
	severity: "warning" | "error";
}

/** GET /suites/{id}/quality. */
export interface QualityReport {
	suite: string;
	tasks: number;
	library_tasks: number;
	duplicates: DuplicatePair[] | null;
	contamination: ContaminationHit[] | null;
	errors: number;
	warnings: number;
	summary: string;
}

export interface SplitPlan {
	held_out_pct: number;
	seed?: number;
	stratify_by?: string;
	time_after?: string;
	confirm?: boolean;
}

export interface SplitChoice {
	task: string;
	stratum?: string;
	was: string;
	split: string;
	public?: string;
	changed?: boolean;
}

/** POST /suites/{id}/held-out. */
export interface HeldOutSplitResult {
	suite: string;
	plan: SplitPlan;
	tasks: SplitChoice[];
	held_out: number;
	public: number;
	canary?: string;
	refused?: string[];
	next: string;
	dry_run: boolean;
	written?: string[];
}

export const ASSIST_KIND = "authoring.assist";
export const ASSIST_OPS = ["rubric", "criterion_wrong", "near_miss_wrong", "exploit"] as const;
export type AssistOp = (typeof ASSIST_OPS)[number];

export interface AssistOpResult {
	op: AssistOp;
	status: "drafted" | "skipped" | "failed";
	reason?: string;
	fields?: string[];
	cost_usd?: number;
}

export interface AssistTaskDraft {
	task: string;
	ops: AssistOpResult[];
	cost_usd: number;
	stopped?: boolean;
}

/** Result of an authoring.assist job. */
export interface AssistResult {
	suite: string;
	estimate: boolean;
	model: string;
	key_id?: string;
	budget_usd: number;
	forecast_usd: number;
	calls: number;
	spent_usd: number;
	tasks: AssistTaskDraft[] | null;
	skipped?: { task: string; reason: string }[];
	pending: number;
	stopped?: boolean;
	next: string;
}
