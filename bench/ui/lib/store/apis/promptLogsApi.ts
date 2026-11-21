import { benchApi } from "./benchApi";

/** The organization's gateway prompt logging setting (handlers/promptlogs.go). */
export interface PromptLogging {
	keep_content: boolean;
	retention_days: number;
	/** The server reaches the shared gateway's log store: rows can be counted and deleted. */
	gateway_logs: boolean;
	max_retention_days: number;
}

export interface PromptLogRow {
	id: string;
	timestamp: string;
	run_id?: string;
	provider?: string;
	model?: string;
	status?: string;
	prompt_tokens: number;
	completion_tokens: number;
	cost_usd: number;
	latency_ms: number;
	content_stored: boolean;
}

export interface PromptLogPage {
	rows: PromptLogRow[];
	total: number;
}

/**
 * Prompt logging per organization: viewers read the setting and the organization's own gateway
 * log rows (metadata only); admins change the setting and delete the rows. The server only ever
 * touches rows tagged with the caller's organization.
 */
export const promptLogsApi = benchApi.enhanceEndpoints({ addTagTypes: ["PromptLogging", "PromptLogs"] }).injectEndpoints({
	endpoints: (builder) => ({
		getPromptLogging: builder.query<PromptLogging, void>({
			query: () => "/prompt-logging",
			providesTags: ["PromptLogging"],
		}),
		setPromptLogging: builder.mutation<PromptLogging, { keep_content: boolean; retention_days: number }>({
			query: (body) => ({ url: "/prompt-logging", method: "PUT", body }),
			invalidatesTags: ["PromptLogging"],
		}),
		listPromptLogs: builder.query<PromptLogPage, { limit?: number; offset?: number } | void>({
			query: (args) => `/prompt-logs?limit=${args?.limit ?? 50}&offset=${args?.offset ?? 0}`,
			providesTags: ["PromptLogs"],
		}),
		deletePromptLogs: builder.mutation<{ deleted: number }, void>({
			query: () => ({ url: "/prompt-logs", method: "DELETE" }),
			invalidatesTags: ["PromptLogs"],
		}),
	}),
});

export const { useGetPromptLoggingQuery, useSetPromptLoggingMutation, useListPromptLogsQuery, useDeletePromptLogsMutation } = promptLogsApi;

/** Retention choices offered, capped at the server's maximum. */
export function retentionChoices(max: number, current: number): number[] {
	const base = [1, 3, 7, 14, 30].filter((d) => d <= max);
	if (current > 0 && current <= max && !base.includes(current)) base.push(current);
	return base.sort((a, b) => a - b);
}
