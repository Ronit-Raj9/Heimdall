import type { WithMode } from "@/lib/types/bench";

/**
 * X6 live scoring (bench/livescore, docs/live-scoring.md). These types mirror the Go JSON of
 * GET /livescore, /livescore/sessions, /livescore/compare and POST /livescore/pull; the key lists in
 * bench/handlers/testdata/contract/Live*.keys pin them (live.contract.test.ts). Live scores are
 * their own evidence class, "live": no ground truth, never part of an experiment verdict.
 */

export type LiveResult = "better" | "worse" | "not_yet_known";

export interface LiveSignals {
	request_errors: number;
	request_error_rate?: number;
	tool_calls?: number;
	tool_errors?: number;
	tool_error_rate?: number;
	repeated_calls?: number;
	loop?: boolean;
	retries: number;
	fallbacks: number;
	refusal?: boolean;
	turn_latency_ms?: number;
	max_turn_latency_ms?: number;
	session_ms?: number;
	cost_usd?: number;
	tokens?: number;
	budget_hits: number;
	rate_limited: number;
	injection_markers?: number;
	abandoned: boolean;
	judge_score?: number;
	judge_calibrated?: boolean;
	content_hidden?: boolean;
}

export interface LiveSessionRecord {
	seq: number;
	id: string;
	version: string;
	evidence: string;
	grouping: string;
	concurrent?: boolean;
	started: string;
	ended: string;
	scored_at: string;
	requests: number;
	model?: string;
	last_row?: string;
	signals: LiveSignals;
	flags?: string[];
	severity?: number;
	signature?: string;
	inbox_item?: string;
}

export interface LiveSignalDef {
	name: string;
	label: string;
	kind: "rate" | "ordinal";
	lower_is_better: boolean;
	unit?: string;
	method: string;
}

export interface LiveSignalMean {
	signal: string;
	mean: number;
	n: number;
}

export interface LiveSeriesBucket {
	start: string;
	sessions: number;
	means: LiveSignalMean[];
}

export interface LiveVersionView {
	name: string;
	selector: string;
	sessions: number;
	flagged: number;
	last_24h: LiveSignalMean[];
	series: LiveSeriesBucket[];
}

export interface LiveSourceView {
	url: string;
	token_env?: string;
	read_only: boolean;
	last_pull?: string;
	since?: string;
	last_error?: string;
	last_scored: number;
	warnings?: string[];
	schedule?: string;
}

export interface LiveGroupingView {
	by_session_id: number;
	by_gap: number;
	concurrent: number;
	gap_seconds: number;
	reliability: string;
}

export interface LivePrivacyView {
	sample_rate: number;
	retention_days: number;
	stores_text: boolean;
	inbox_redacted: boolean;
	inbox_push: boolean;
	inbox_max_per_pull: number;
}

export interface LiveJudgeView {
	model: string;
	sample_rate: number;
	calibrated: boolean;
	note: string;
}

export interface LiveSignalResult {
	name: string;
	label: string;
	kind: "rate" | "ordinal";
	lower_is_better: boolean;
	unit?: string;
	pairs: number;
	need_pairs: number;
	sessions_baseline: number;
	sessions_candidate: number;
	mean_baseline?: number;
	mean_candidate?: number;
	e_worse: number;
	e_better: number;
	decision: LiveResult;
	decided_at?: string;
	dropped?: number;
}

export interface LiveComparison {
	evidence: string;
	baseline: string;
	candidate: string;
	result: LiveResult;
	alpha: number;
	per_test_level: number;
	threshold: number;
	min_pairs: number;
	started_at: string;
	decided_at?: string;
	sessions_baseline: number;
	sessions_candidate: number;
	restarts: number;
	signals: LiveSignalResult[];
	method: string;
	stale?: string;
}

export interface LiveLogEvent {
	at: string;
	event: string;
	detail?: string;
}

export interface LiveStatus {
	evidence: string;
	note: string;
	configured: boolean;
	problem?: string;
	source?: LiveSourceView;
	versions: LiveVersionView[];
	signals: LiveSignalDef[];
	grouping: LiveGroupingView;
	compare?: LiveComparison;
	worst: LiveSessionRecord[];
	events: LiveLogEvent[];
	privacy: LivePrivacyView;
	judge?: LiveJudgeView;
}

export interface LiveSessionList {
	evidence: string;
	total: number;
	sessions: LiveSessionRecord[];
}

export interface LivePullResult {
	at: string;
	window: [string, string];
	rows: Record<string, number>;
	sessions: Record<string, number>;
	skipped: number;
	open: number;
	details: number;
	unchecked?: number;
	overlap?: number;
	inbox: string[];
	alerts: string[];
	decided: string[];
	pruned?: number;
	truncated: boolean;
	warnings?: string[];
	judged?: number;
	errors?: Record<string, string>;
}

export type LiveStatusResponse = WithMode<LiveStatus>;
export type LiveSessionListResponse = WithMode<LiveSessionList>;
export type LiveComparisonResponse = WithMode<LiveComparison>;
export type LivePullResponse = WithMode<LivePullResult>;
