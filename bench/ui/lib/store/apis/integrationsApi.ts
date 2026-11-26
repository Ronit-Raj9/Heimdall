import type {
	CIWorkflowParams,
	CIWorkflowPreview,
	EmailPrefs,
	EmailSettings,
	EmailTestResult,
	IntegrationExportList,
	Webhook,
	WebhookCreated,
	WebhookDelivery,
	WebhookInput,
	WebhookList,
} from "@/lib/types/integrations";
import { transportErrorText } from "@/lib/requestErrors";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

/** Integrations area (web parity L19): webhooks, export files, CI workflow preview. The CI-install PR
 * and repository list are githubApi (Settings > GitHub, handlers/github.go). */
export const integrationsApi = benchApi.enhanceEndpoints({ addTagTypes: ["IntegrationWebhooks", "IntegrationExports", "EmailSettings"] }).injectEndpoints({
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
		getEmailSettings: builder.query<EmailSettings, void>({
			query: () => "/notifications/email",
			providesTags: ["EmailSettings"],
		}),
		putEmailSettings: builder.mutation<EmailSettings, Pick<EmailPrefs, "events" | "digest">>({
			query: (body) => ({ url: "/notifications/email", method: "PUT", body }),
			invalidatesTags: ["EmailSettings"],
		}),
		testEmail: builder.mutation<EmailTestResult, void>({
			query: () => ({ url: "/notifications/email/test", method: "POST" }),
		}),
		getCIWorkflow: builder.query<CIWorkflowPreview, CIWorkflowParams>({
			query: (args) => ({ url: "/integrations/ci/workflow", params: ciWorkflowParams(args) }),
		}),
	}),
});

/** The query of the CI workflow preview without empty values. */
export function ciWorkflowParams(args: CIWorkflowParams): Record<string, string> {
	const out: Record<string, string> = {};
	for (const [k, v] of Object.entries(args)) {
		if (typeof v === "string" && v !== "") out[k] = v;
	}
	return out;
}

/** The download URL of an export file. */
export function exportDownloadUrl(name: string): string {
	return `${BENCH_API_BASE}/integrations/exports/${encodeURIComponent(name)}`;
}

/** The message of an API error: the server's error.message, else a fallback. */
export function apiErrorMessage(error: unknown, fallback: string): string {
	const transport = transportErrorText(error);
	if (transport) return transport;
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
	useGetEmailSettingsQuery,
	usePutEmailSettingsMutation,
	useTestEmailMutation,
} = integrationsApi;
