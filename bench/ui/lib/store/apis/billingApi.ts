import type { BillingUsageReport, BillingView, Invoice, SeatSync } from "@/lib/types/billing";
import { benchApi } from "./benchApi";
import { usageParams } from "./usageApi";

// Plans, metering and billing (lane Y16): the org's plan and meters, metered usage per day, and the
// Stripe checkout, portal, invoices and seat sync. Checkout and portal answer a URL the page opens.
export const billingApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchBilling"] }).injectEndpoints({
	endpoints: (builder) => ({
		getBilling: builder.query<BillingView, void>({
			query: () => "/billing",
			providesTags: ["BenchBilling"],
		}),
		getBillingUsage: builder.query<BillingUsageReport, { from?: string; to?: string }>({
			query: (p) => ({ url: "/billing/usage", params: usageParams(p) }),
			providesTags: ["BenchBilling"],
		}),
		getInvoices: builder.query<{ invoices: Invoice[] }, void>({
			query: () => "/billing/invoices",
			providesTags: ["BenchBilling"],
		}),
		startCheckout: builder.mutation<{ url: string }, void>({
			query: () => ({ url: "/billing/checkout", method: "POST" }),
		}),
		openPortal: builder.mutation<{ url: string }, void>({
			query: () => ({ url: "/billing/portal", method: "POST" }),
		}),
		syncSeats: builder.mutation<SeatSync, void>({
			query: () => ({ url: "/billing/seats/sync", method: "POST" }),
			invalidatesTags: ["BenchBilling"],
		}),
	}),
});

export const { useGetBillingQuery, useGetBillingUsageQuery, useGetInvoicesQuery, useStartCheckoutMutation, useOpenPortalMutation, useSyncSeatsMutation } =
	billingApi;
