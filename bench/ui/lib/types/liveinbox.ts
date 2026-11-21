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
	problem?: string;
	next?: string;
	example: string;
	sources_snippet?: string;
	mode?: string;
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
