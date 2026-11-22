/** Trace search and the Maxim integration (lane X23). Shapes mirror the Go handlers. */

export type SearchMatchField = "step" | "tool_call" | "answer" | "gate" | "grade" | "event" | "error";

export interface SearchMatch {
	field: SearchMatchField | (string & {});
	seq?: number;
	label?: string;
	text: string;
	/** [start, end) code point offsets into text. */
	highlights: [number, number][];
}

export interface SearchHit {
	run_id: string;
	experiment: string;
	benchmark: string;
	task: string;
	agent: string;
	version?: string;
	model?: string;
	outcome: string;
	started?: string;
	score: number;
	matches: SearchMatch[];
}

export interface SearchResponse {
	query: string;
	total: number;
	truncated: boolean;
	indexed_runs: number;
	hits: SearchHit[];
}

export interface SearchParams {
	q: string;
	agent?: string;
	version?: string;
	benchmark?: string;
	outcome?: string;
	limit?: number;
}

export interface MaximConnection {
	connected: boolean;
	workspace_id: string;
	repo_id: string;
	auto_push: boolean;
	link_template: string;
	key_last4?: string;
	updated_at?: string;
	pushed: number;
	failed: number;
	last_error?: string;
}

export interface MaximConnectionUpdate {
	/** Omit or leave empty to keep the stored key. */
	api_key?: string;
	workspace_id: string;
	repo_id: string;
	auto_push: boolean;
	link_template?: string;
}

export interface MaximTestResult {
	ok: boolean;
	message: string;
	repo_name?: string;
}

export type RunMaximStatus = "pushed" | "failed" | "not_pushed";

export interface RunMaxim {
	connected: boolean;
	status: RunMaximStatus;
	trace_id: string;
	url?: string;
	error?: string;
	pushed_at?: string;
}