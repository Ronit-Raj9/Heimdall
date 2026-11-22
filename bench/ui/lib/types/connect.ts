// The Connect box (lane Z01): mirrors bench/connect and handlers/connect.go.

import type { EvaluationSpec } from "@/lib/store/apis/evaluationsApi";

export type ConnectClass = "url" | "curl" | "repository" | "image" | "platform" | "socket" | "phone" | "pull_object" | "trace" | "unknown";

export interface ConnectQuestion {
	kind: "secret" | "text" | "pick_field";
	/** The template param the answer fills. */
	param: string;
	label: string;
	help?: string;
	/** Suggested secret name. */
	secret?: string;
	header?: string;
	/** A value pasted in the same input (a token in a curl command); never written to a config. */
	prefill?: string;
	options?: string[];
}

/** Lane Y10 schemas.RemoteMapping. */
export interface RemoteStep {
	method?: string;
	url: string;
	headers?: Record<string, string>;
	body?: string;
	id_path?: string;
}

export interface RemoteMapping {
	preset?: string;
	method?: string;
	headers?: Record<string, string>;
	query?: Record<string, string>;
	body?: string;
	answer_path?: string;
	stream?: "sse" | "aws_eventstream";
	stream_path?: string;
	stream_decode?: "base64";
	start?: RemoteStep;
	poll?: { url: string; answer_path: string; interval_s?: number; reply_id_path?: string };
	end?: RemoteStep;
}

/** Lane Y10 schemas.RemoteAuth (secret names only). */
export interface RemoteAuth {
	type?: string;
	token_url?: string;
	tenant_id?: string;
	client_id_secret?: string;
	client_secret_secret?: string;
	scope?: string;
	audience?: string;
	access_key_secret?: string;
	secret_key_secret?: string;
	session_token_secret?: string;
	region?: string;
	service?: string;
	service_account_secret?: string;
}

export interface ConnectFix {
	action: string;
	label: string;
	value?: string;
}

export interface ConnectFailure {
	code: string;
	title: string;
	message: string;
	fix?: ConnectFix;
	infra?: boolean;
	options?: string[];
}

export interface ConnectCandidate {
	kind: string;
	preset?: string;
	label: string;
	confidence: number;
	evidence: string[];
	supported: boolean;
	message?: string;
	template?: string;
	url?: string;
	params?: Record<string, string>;
	mapping?: RemoteMapping;
	auth?: RemoteAuth;
	nonce_found: boolean;
	questions?: ConnectQuestion[];
	capabilities?: string[];
	command?: string;
}

export interface ConnectProbe {
	method: string;
	url: string;
	status?: number;
	ms: number;
	note?: string;
}

export interface ConnectPreview {
	id: string;
	path: string;
	yaml?: string;
	error?: string;
}

export interface ConnectDetectRequest {
	input: string;
	platform?: string;
	id?: string;
	secrets?: Record<string, string>;
}

export interface ConnectDetectResponse {
	class: ConnectClass;
	candidates: ConnectCandidate[];
	failures?: ConnectFailure[];
	probes: ConnectProbe[];
	nonce?: string;
	preview?: ConnectPreview;
	from: string;
}

export interface ConnectSuggest {
	history?: "server" | "client" | "none";
	sessions?: boolean;
	timeout_s?: number;
	streams?: boolean;
}

export interface ConnectSaveRequest {
	id: string;
	candidate: ConnectCandidate;
	notes?: string;
	suggest?: ConnectSuggest;
	dry_run?: boolean;
	overwrite?: boolean;
}

export interface ConnectSaveResponse {
	id: string;
	path: string;
	files: string[];
	yaml: string;
	dry_run: boolean;
}

export type VerifyRowStatus = "pending" | "running" | "pass" | "warn" | "fail" | "skip";

export interface VerifyRow {
	id: string;
	title: string;
	status: VerifyRowStatus;
	detail?: string;
	failure?: ConnectFailure;
	ms?: number;
}

export interface VerifyResult {
	agent?: string;
	ok: boolean;
	rows: VerifyRow[];
	suggest: ConnectSuggest;
	version?: string;
	nonce?: string;
}

export interface ConnectVerifyRequest {
	agent: string;
	suite?: string;
	task?: string;
	skip_real_task?: boolean;
}

export interface ConnectVerifyStatus {
	job_id?: string;
	status: string;
	agent: string;
	suite?: string;
	rows: VerifyRow[];
	result?: VerifyResult;
	error?: string;
}

export interface BenchmarkInfo {
	id: string;
	compatible: boolean;
	reason?: string;
	needs_model: boolean;
	tasks: number;
	estimate_s: number;
	estimate_basis?: string;
	reference?: boolean;
}

export interface FirstLookPick {
	benchmark: string;
	reason: string;
	tasks: number;
	trials: number;
	concurrency: number;
	estimate_s: number;
	note: string;
	considered: BenchmarkInfo[];
}

export interface ConnectFirstLookResponse {
	pick: FirstLookPick;
	evaluation: EvaluationSpec;
	reference?: { score?: number; ci_low?: number; ci_high?: number; agent?: string; model?: string } | null;
	/** Optional second card: the agent against the bare model, same benchmark and size (H06). */
	bare_model?: EvaluationSpec;
	bare_model_reason?: string;
}

export interface ConnectPlatform {
	id: string;
	label: string;
	id_label: string;
	help: string;
}

export interface ConnectCatalog {
	failures: { code: string; title: string; trigger: string; message: string; action?: string; label?: string; infra?: boolean }[];
	platforms: ConnectPlatform[];
}

// Types the "connect" namespace against its English catalog.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		connect: typeof import("@/locales/en/connect.json");
	}
}