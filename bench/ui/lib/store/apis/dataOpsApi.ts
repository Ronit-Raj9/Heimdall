// Data operations (lane X16): delete a comparison or an evaluation, the data retention policy,
// the webhook delivery log, organization deletion and the audit log export and integrity check.
// Injected into benchApi so writes share its base query (CSRF header, org header, 401 handling).
import type {
	AuditExportFormat,
	AuditVerifyResult,
	DeletedResponse,
	DeliveryList,
	OrgDeleteResponse,
	RetentionPolicy,
} from "@/lib/types/dataops";
import type { WebhookDelivery } from "@/lib/types/integrations";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";
import { withOrgParam } from "./orgParam";

const id = (value: string) => encodeURIComponent(value);

/** How often the delivery log refreshes while a delivery is waiting for another attempt. */
export const DELIVERY_POLL_MS = 10_000;

/**
 * Download URL of the organization's audit log (the server answers with an attachment). The
 * optional filters (action, actor type, search text) keep only the rows that match.
 */
export function auditExportUrl(orgId: string, format: AuditExportFormat, f?: { action?: string; actor?: string; q?: string }): string {
	const params = new URLSearchParams({ format });
	const action = f?.action?.trim();
	const actor = f?.actor?.trim();
	const q = f?.q?.trim();
	if (action) params.set("action", action);
	if (actor) params.set("actor_type", actor);
	if (q) params.set("q", q);
	return withOrgParam(`${BENCH_API_BASE}/orgs/${id(orgId)}/audit/export?${params.toString()}`);
}

export const dataOpsApi = benchApi
	.enhanceEndpoints({ addTagTypes: ["Evaluations", "DataRetention", "WebhookDeliveries", "IntegrationWebhooks"] })
	.injectEndpoints({
		endpoints: (builder) => ({
			deleteExperiment: builder.mutation<DeletedResponse, string>({
				query: (expId) => ({ url: `/experiments/${id(expId)}`, method: "DELETE" }),
				invalidatesTags: [{ type: "BenchExperiment", id: "LIST" }, "AuditLog"],
			}),
			deleteEvaluation: builder.mutation<DeletedResponse, string>({
				query: (evalId) => ({ url: `/evaluations/${id(evalId)}`, method: "DELETE" }),
				invalidatesTags: [{ type: "Evaluations", id: "LIST" }, { type: "BenchExperiment", id: "LIST" }, "AuditLog"],
			}),
			getRetention: builder.query<RetentionPolicy, void>({
				query: () => "/data/retention",
				providesTags: ["DataRetention"],
			}),
			setRetention: builder.mutation<RetentionPolicy, number>({
				query: (runDays) => ({ url: "/data/retention", method: "PUT", body: { run_days: runDays } }),
				invalidatesTags: ["DataRetention", "AuditLog"],
			}),
			listDeliveries: builder.query<DeliveryList, number | void>({
				query: (limit) => ({ url: "/integrations/deliveries", params: { limit: limit ?? 100 } }),
				transformResponse: (raw: unknown) => {
					const body = (raw ?? {}) as DeliveryList;
					return { deliveries: Array.isArray(body.deliveries) ? body.deliveries : [] };
				},
				providesTags: ["WebhookDeliveries"],
			}),
			retryDelivery: builder.mutation<{ delivery: WebhookDelivery }, string>({
				query: (deliveryId) => ({ url: `/integrations/deliveries/${id(deliveryId)}/retry`, method: "POST" }),
				invalidatesTags: ["WebhookDeliveries", "IntegrationWebhooks"],
			}),
			deleteOrg: builder.mutation<OrgDeleteResponse, { orgId: string; confirm: string }>({
				query: ({ orgId, confirm }) => ({ url: `/orgs/${id(orgId)}/delete`, method: "POST", body: { confirm } }),
			}),
			verifyAudit: builder.mutation<AuditVerifyResult, string>({
				// A GET, but run on demand from a button, so it is a mutation-shaped trigger.
				query: (orgId) => ({ url: `/orgs/${id(orgId)}/audit/verify`, method: "GET" }),
			}),
		}),
	});

export const {
	useDeleteExperimentMutation,
	useDeleteEvaluationMutation,
	useGetRetentionQuery,
	useSetRetentionMutation,
	useListDeliveriesQuery,
	useRetryDeliveryMutation,
	useDeleteOrgMutation,
	useVerifyAuditMutation,
} = dataOpsApi;