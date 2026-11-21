import type {
	CIInstallRequest,
	CIInstallResult,
	CIWorkflowPreview,
	GithubRepo,
	IntegrationExportList,
	Webhook,
	WebhookCreated,
	WebhookDelivery,
	WebhookInput,
	WebhookList,
} from "@/lib/types/integrations";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

/** Integrations area (web parity L19): webhooks, export files, CI install. */
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
		listGithubRepos: builder.query<GithubRepo[], void>({
			query: () => "/github/repos",
			transformResponse: (body: unknown) => parseRepos(body),
		}),
		installCI: builder.mutation<CIInstallResult, CIInstallRequest>({
			query: (body) => ({ url: "/github/ci-install", method: "POST", body }),
		}),
	}),
});

/** Reads GET /github/repos as an array or {repos: [...]}, each with full_name or owner/name. */
export function parseRepos(body: unknown): GithubRepo[] {
	const list = Array.isArray(body) ? body : ((body as { repos?: unknown[] } | null)?.repos ?? []);
	const out: GithubRepo[] = [];
	for (const r of list as Record<string, unknown>[]) {
		if (!r || typeof r !== "object") continue;
		const owner = typeof r.owner === "string" ? r.owner : ((r.owner as { login?: string } | undefined)?.login ?? "");
		const full = typeof r.full_name === "string" ? r.full_name : owner && typeof r.name === "string" ? `${owner}/${r.name}` : "";
		if (full.includes("/")) out.push({ full_name: full, default_branch: typeof r.default_branch === "string" ? r.default_branch : undefined });
	}
	return out;
}

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
	useListGithubReposQuery,
	useInstallCIMutation,
} = integrationsApi;
