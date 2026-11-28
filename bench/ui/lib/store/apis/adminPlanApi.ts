import { benchApi } from "./benchApi";

export const ORG_PLANS = ["free", "team", "enterprise"] as const;
export type OrgPlan = (typeof ORG_PLANS)[number];

/** Answer of PUT /admin/billing/{id} (handlers/billing.go AdminBillingAccount). */
export interface AdminBillingAccount {
	org_id: string;
	plan: string;
	effective_plan: string;
	status: string;
}

/** PUT /admin/billing/{id} with SetPlanBody: server admins only. */
export function setOrgPlanRequest(orgId: string, plan: OrgPlan) {
	return { url: `/admin/billing/${encodeURIComponent(orgId)}`, method: "PUT", body: { plan } } as const;
}

/** Round 5 lane L8: set an organization's plan by hand. */
export const adminPlanApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		setOrgPlan: builder.mutation<AdminBillingAccount, { orgId: string; plan: OrgPlan }>({
			query: ({ orgId, plan }) => setOrgPlanRequest(orgId, plan),
		}),
	}),
});

export const { useSetOrgPlanMutation } = adminPlanApi;
