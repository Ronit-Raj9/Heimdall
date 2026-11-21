import type {
	CIWorkflowPreview,
	IntegrationExportList,
	Webhook,
	WebhookCreated,
	WebhookDelivery,
	WebhookInput,
	WebhookList,
} from "@/lib/types/integrations";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

/** Integrations area (web parity L19): webhooks, export files, CI workflow preview. The CI-install PR
 * and repository list are githubApi (Settings > GitHub, handlers/github.go). */
export const integrationsApi = benchApi.enhanceEndpoints({ addTagTypes: ["IntegrationWebhooks", "IntegrationExports"] }).injectEndpoints({
	endpoints: (builder) => ({
		listWebhooks: builder.query<WebhookList, void>({
			query: () => "/integrations/webhooks",
			providesTags: ["IntegrationWebhooks"],
		}),
		createWebhook: builder.mutation<WebhookCreated, WebhookInput>({
			query: (body) => ({ url: "/integrations/webhooks", method: "POST", body }),
			invalidatesTags: ["IntegrationWebhooks"],
		}),
		updateWebhook: builder.mutation<Webhook, { id: string; body: WebhookInput }>({
			query: ({ id, body }) => ({ url: `/integrations/webhooks/${encodeURIComponent(id)}`, method: "PUT", body }),
			invalidatesTags: ["IntegrationWebhooks"],
		}),
		deleteWebhook: builder.mutation<void, string>({
			query: (id) => ({ url: `/integrations/webhooks/${encodeURIComponent(id)}`, method: "DELETE" }),
			invalidatesTags: ["IntegrationWebhooks"],
		}),
		testWebhook: builder.mutation<WebhookDelivery, string>({
			query: (id) => ({ url: `/integrations/webhooks/${encodeURIComponent(id)}/test`, method: "POST" }),
			invalidatesTags: ["IntegrationWebhooks"],
		}),
		listExports: builder.query<IntegrationExportList, void>({
			query: () => "/integrations/exports",
			providesTags: ["IntegrationExports"],
		}),
		deleteExport: builder.mutation<void, string>({
			query: (name) => ({ url: `/integrations/exports/${encodeURIComponent(name)}`, method: "DELETE" }),
			invalidatesTags: ["IntegrationExports"],
		}),
		getCIWorkflow: builder.query<CIWorkflowPreview, { mode: string; agent?: string }>({
			query: ({ mode, agent }) => ({ url: "/integrations/ci/workflow", params: agent ? { mode, agent } : { mode } }),
		}),
	}),
});

/** The download URL of an export file. */
export function exportDownloadUrl(name: string): string {
	return `${BENCH_API_BASE}/integrations/exports/${encodeURIComponent(name)}`;
}

/** The message of an API error: the server's error.message, else a fallback. */
export function apiErrorMessage(error: unknown, fallback: string): string {
	const e = error as { status?: number | string; data?: { error?: { message?: string } } } | undefined;
	return e?.data?.error?.message ?? fallback;
}

export const {
	useListWebhooksQuery,
	useCreateWebhookMutation,
	useUpdateWebhookMutation,
	useDeleteWebhookMutation,
	useTestWebhookMutation,
	useListExportsQuery,
	useDeleteExportMutation,
	useGetCIWorkflowQuery,
} = integrationsApi;
