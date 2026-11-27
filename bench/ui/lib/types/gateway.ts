import type { Experiment } from "./bench";

/** W4 Bifrost feature tests (audit/W4_DESIGN_3_BIFROST.md). Mirrors bench/gwfeature and the gateway routes. */
export interface GwInterval {
	point: number;
	low?: number;
	high?: number;
}
export interface GwSource {
	kind: string;
	exp_id?: string;
	runs: number;
	rows: number;
	raw_rows: number;
	gateway?: string;
	text: string;
}
export interface GwSetting {
	kind: "routing_rule" | "request_headers" | "plugin_config" | "virtual_key" | "none";
	title: string;
	where: string;
	body: string;
	note?: string;
}
export interface GwArmResult {
	arm: string;
	label: string;
	runs: number;
	tasks: number;
	solve_rate?: number;
	cost_per_run?: number;
	latency_ms?: number;
}
export type GwState = "measured" | "not_enough_data" | "not_applied" | "unknown";
export type GwResult = "saved" | "lost" | "worked" | "failed";
export interface GwEffect {
	feature: string;
	exp_id: string;
	control: string;
	variant?: string;
	state: GwState;
	reason?: string;
	headline: string;
	result?: GwResult;
	tasks: number;
	solve_delta?: GwInterval;
	cost_delta?: GwInterval;
	latency_delta?: GwInterval;
	arms: GwArmResult[];
	details?: Record<string, unknown>;
	setting?: GwSetting;
	source: GwSource;
	fingerprint: string;
	computed_at: string;
}
export interface GwFeature {
	id: string;
	name: string;
}
export interface GwDraftRequest {
	feature: string;
	suite: string;
	base: string;
	model?: string;
	provider?: string;
	tasks?: string[];
	trials?: number;
	budget_usd: number;
	options?: Record<string, string>;
}
export interface GwDraftResponse {
	experiment: Experiment;
	notes: string[];
}
export interface GwSourceInfo {
	connected: boolean;
	host: string;
	mode: "saved" | "run_gateway" | "none";
	error: string;
}
export interface GwGatewayInfo {
	configured: boolean;
	host?: string;
	version?: string;
	reachable: boolean;
	error?: string;
	plugins: string[];
}
export interface GwCardLine {
	feature: string;
	name: string;
	enabled: "on" | "off" | "unknown";
	enabled_source: string;
	state: GwState | "not_measured";
	result?: GwResult;
	headline: string;
	source?: GwSource;
	exp_id?: string;
	measured_at?: string;
	start?: string;
}
export interface GwReportCard {
	gateway: GwGatewayInfo;
	source: GwSourceInfo;
	lines: GwCardLine[];
	checked_at: string;
}
export interface GwBifrostSettingAnswer {
	setting: GwSetting | null;
	reason: string;
}
export interface GwRegisteredAgent {
	name: string;
	card_url: string;
	enabled: boolean;
}
export interface GwWatchItem {
	provider: string;
	model: string;
	first_seen: string;
	state: "claimed" | "started" | "failed" | "skipped";
	evaluation_id?: string;
	reason?: string;
	claimed_at: string;
}
export interface GwWatchState {
	schedule: string;
	baseline: Record<string, string[]>;
	seen: Record<string, GwWatchItem>;
	checked_at: string;
	last_error?: string;
}
export interface GwPrompt {
	id: string;
	name: string;
}
export interface GwPromptVersion {
	version: number;
	commit_message: string;
	created_at: string;
	messages: number;
	model: string;
}
export interface GwSkill {
	id: string;
	name: string;
	latest_version: string;
}
export interface GwSkillVersion {
	version: string;
	created_at: string;
	size: number;
}