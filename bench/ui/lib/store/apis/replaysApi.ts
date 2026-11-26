import { benchApi } from "./benchApi";

/** The seven result states of a replayed step (shadowreplay/replay.go). */
export const REPLAY_STATES = [
	"equivalent_response",
	"different_response",
	"different_tool_call",
	"missing_recorded_tool_result",
	"redacted_input",
	"insufficient_evidence",
	"replay_infrastructure_failure",
] as const;
export type ReplayState = (typeof REPLAY_STATES)[number];

/** replays/settings.json: the org's opt-in and caps. */
export interface ReplaySettings {
	enabled: boolean;
	enabled_by: string;
	enabled_at: string | null;
	max_sessions_per_run: number;
	max_usd_per_run: number;
}

export interface ReplayStep {
	index: number;
	state: string;
	basis?: string;
	recorded: string;
	new?: string;
	judge_note?: string;
	cost_usd: number;
	latency_ms: number;
	recorded_cost_usd?: number;
	recorded_latency_ms?: number;
}

export interface ReplaySession {
	recording: string;
	session_id: string;
	steps: ReplayStep[];
	final: string;
	diverged_at?: number;
	tool_agreement?: number;
	write_set_equal?: boolean;
	cost_usd: number;
	recorded_cost_usd?: number;
	inbox_item?: string;
}

export interface ReplayStats {
	compared: number;
	left_out: number;
	shares: Record<string, number>;
	equivalent_low: number;
	equivalent_high: number;
	paired: number;
	median_cost_usd: number;
	median_recorded_cost_usd: number;
	median_latency_ms: number;
	median_recorded_latency_ms: number;
	paired_latency: number;
}

export interface ReplayReport {
	id: string;
	mode: string;
	candidate: string;
	recorded: string[] | null;
	sessions: ReplaySession[];
	counts: Record<string, number>;
	spent_usd: number;
	stopped?: string;
	note: string;
	stats?: ReplayStats;
	created_at: string;
}

export interface ReplayRow {
	id: string;
	mode: string;
	candidate: string;
	sessions: number;
	counts: Record<string, number> | null;
	spent_usd: number;
	created_at: string;
}

export interface ReplayInboxResult {
	item_id: string;
	created: boolean;
	occurrences: number;
}

/** Shadow replay (handlers/replays_w447.go). Replays start with POST /jobs kind replay.run. */
export const replaysApi = benchApi.enhanceEndpoints({ addTagTypes: ["ReplaySettings", "Replays"] }).injectEndpoints({
	endpoints: (builder) => ({
		getReplaySettings: builder.query<ReplaySettings, void>({
			query: () => "/replays/settings",
			providesTags: ["ReplaySettings"],
		}),
		putReplaySettings: builder.mutation<ReplaySettings, ReplaySettings>({
			query: (body) => ({ url: "/replays/settings", method: "PUT", body }),
			invalidatesTags: (_r, error) => (error ? [] : ["ReplaySettings"]),
		}),
		getReplays: builder.query<{ replays: ReplayRow[] }, void>({
			query: () => "/replays",
			providesTags: ["Replays"],
		}),
		getReplay: builder.query<ReplayReport, string>({
			query: (id) => `/replays/${encodeURIComponent(id)}`,
			providesTags: (_r, _e, id) => [{ type: "Replays", id }],
		}),
		replayToInbox: builder.mutation<ReplayInboxResult, { id: string; recording: string }>({
			query: ({ id, recording }) => ({
				url: `/replays/${encodeURIComponent(id)}/sessions/${encodeURIComponent(recording)}/to-inbox`,
				method: "POST",
			}),
			invalidatesTags: (_r, error, { id }) => (error ? [] : [{ type: "Replays", id }]),
		}),
	}),
});

export const { useGetReplaySettingsQuery, usePutReplaySettingsMutation, useGetReplaysQuery, useGetReplayQuery, useReplayToInboxMutation } =
	replaysApi;