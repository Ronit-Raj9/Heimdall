/** GET /inbox/webhook (handlers/liveinbox_webhook.go): how a tool pushes failures into the inbox. Never carries the token. */
export interface InboxWebhookInfo {
	url: string;
	enabled: boolean;
	configured: boolean;
	source?: string;
	token_env?: string;
	token_set: boolean;
	rate_per_minute?: number;
	max_bytes?: number;
	hosted: boolean;
	/** True on the hosted server: per-org ingest tokens at /inbox/tokens. */
	ingest_tokens?: boolean;
	problem?: string;
	next?: string;
	example: string;
	sources_snippet?: string;
	mode?: string;
}

/** One inbox ingest token as listed (handlers/inboxingest.go): never the token itself. */
export interface InboxToken {
	id: string;
	org_id: string;
	label: string;
	last4: string;
	created_by?: string;
	created_at: string;
	last_used_at?: string;
}

/** GET /inbox/tokens. */
export interface InboxTokenList {
	tokens: InboxToken[];
	url: string;
	max: number;
}

/** POST /inbox/tokens: the only answer that carries the token (secret). */
export interface InboxTokenCreated {
	token: InboxToken;
	secret: string;
	url: string;
	example: string;
}

/** Params of the livescore.init job (cmd/bifrost-bench/jobs_live.go). */
export interface LiveInitParams {
	url?: string;
	token_env?: string;
	baseline_key?: string;
	candidate_key?: string;
	tag_header?: string;
	baseline_tag?: string;
	candidate_tag?: string;
	force?: boolean;
}

export type LiveSplit = "keys" | "tag" | "later";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		liveinbox: typeof import("@/locales/en/liveinbox.json");
	}
}

/** One thing to do before a pull works (handlers/liveinbox_pull.go PullStep). */
export interface PullStep {
	problem: string;
	action: string;
	/** "/settings?tab=secrets", "/models", "#live-setup" or "#inbox-source-setup". */
	link?: string;
	secret?: string;
}

/** A credential a pull reads, by name only. */
export interface PullSecret {
	name: string;
	for: string;
	set: boolean;
}

/** Whether one kind of pull can run now. */
export interface PullCheck {
	ready: boolean;
	configured: boolean;
	secrets: PullSecret[];
	steps: PullStep[];
	judge?: { key_ready: boolean; next?: string; link?: string };
}

/** GET /liveinbox/pull. */
export interface PullReadiness {
	hosted: boolean;
	live: PullCheck;
	inbox: PullCheck;
}

/** Params of the inbox.source job (cmd/bifrost-bench/jobs_inbox_source.go). */
export interface InboxSourceParams {
	name: string;
	kind: "bifrost" | "langfuse" | "langsmith";
	url?: string;
	host?: string;
	token_env?: string;
	public_key_env?: string;
	secret_key_env?: string;
	api_key_env?: string;
	project?: string;
}
