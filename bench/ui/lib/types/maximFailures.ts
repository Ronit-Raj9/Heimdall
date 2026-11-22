// Maxim production failures to regression tests (lane Z06): the wire types of
// /integrations/maxim/batches (handlers/maximtasks.go, bench/tracebulk).

export interface MaximFailureFilter {
	evaluators?: string[];
	errors?: boolean;
	score_below?: number;
	score_evaluators?: string[];
	days?: number;
	limit?: number;
}

export interface MaximFailureCounts {
	scanned: number;
	failed: number;
	evaluator_failed: number;
	errored: number;
	low_score: number;
	fetched: number;
	new_items: number;
	repeats: number;
}

export interface MaximFailureCluster {
	id: string;
	label: string;
	tool?: string;
	error?: string;
	evaluator?: string;
	items: string[];
	occurrences: number;
	sample: string;
	reasons?: string[];
	links?: string[];
	kind: string;
}

export type MaximTaskState = "pending_review" | "needs_files" | "accepted" | "rejected";
export type MaximReadinessStatus = "not_run" | "queued" | "ready" | "not_ready";

export interface MaximTaskDraft {
	cluster: string;
	item: string;
	suite: string;
	id: string;
	kind: string;
	state: MaximTaskState;
	generated: string[];
	edit_me?: string[];
	request: string;
	recorded: string;
	suggested_rubric: string;
	readiness: { status: MaximReadinessStatus; job_id?: string; at?: string };
	drafted: string;
	reviewed?: string;
	reviewed_by?: string;
	note?: string;
}

export interface MaximFailureBatch {
	id: string;
	source: string;
	workspace: string;
	repo: string;
	filter: MaximFailureFilter;
	counts: MaximFailureCounts;
	warnings?: string[];
	truncated?: boolean;
	clusters: MaximFailureCluster[];
	tasks: MaximTaskDraft[];
	created: string;
	created_by?: string;
	updated: string;
}

export interface MaximFailureSummary {
	id: string;
	source: string;
	repo: string;
	counts: MaximFailureCounts;
	clusters: number;
	tasks: number;
	pending: number;
	created: string;
}

export interface MaximFailureList {
	connected: boolean;
	batches: MaximFailureSummary[];
}

export interface MaximDraftRequest {
	suite: string;
	clusters?: string[];
	per_cluster?: number;
	kind?: string;
}

export interface MaximDraftResult {
	batch: MaximFailureBatch;
	tasks: MaximTaskDraft[];
	warnings: string[];
}

export interface MaximReviewRequest {
	answer?: string;
	rubric?: string;
	instruction?: string;
	skip_readiness?: boolean;
}

export interface MaximTaskResult {
	task: MaximTaskDraft;
	message?: string;
}
