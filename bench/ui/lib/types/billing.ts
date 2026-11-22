// Plans, metering and billing (lane Y16): bodies of the /billing routes and of
// GET /api/public/pricing (billing/service.go, handlers/billing.go).

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		billing: typeof import("@/locales/en/billing.json");
	}
}

export type PlanId = "free" | "team" | "enterprise";
export type BillingFeature = "private_benchmarks" | "ci" | "alerts" | "own_worker_support" | "invoice_billing";

export interface Plan {
	id: PlanId;
	name: string;
	summary: string;
	/** Monthly price per seat; 0 for Free. */
	price_usd: number;
	price_unit: "seat_month" | "free" | "contact";
	contact: boolean;
	/** 0: no limit. */
	max_seats: number;
	/** Monthly shared worker minutes; 0: no limit. */
	shared_worker_minutes: number;
	features: BillingFeature[];
	own_key: boolean;
}

export type MeterId = "shared_worker_minutes" | "seats" | "runs" | "evaluations" | "storage_bytes" | "operator_judge_calls";

export interface MeterView {
	id: MeterId;
	used: number;
	/** 0: no limit. */
	limit: number;
	unit: "minutes" | "seats" | "runs" | "evaluations" | "bytes" | "calls";
	period: "month" | "now";
	enforced: boolean;
	reached: boolean;
	available: boolean;
}

export interface BillingView {
	enabled: boolean;
	stripe_mode: "test" | "live" | "";
	checkout_ready: boolean;
	checkout_blocker?: string;
	portal_ready: boolean;
	plan: Plan;
	status: string;
	paid_seats: number;
	period_end?: string;
	period_from: string;
	period_to: string;
	meters: MeterView[];
	features: { id: BillingFeature; included: boolean }[];
	contact?: string;
	plans: Plan[];
}

export interface UsageDay {
	day: string;
	shared_worker_minutes: number;
	runs: number;
	evaluations: number;
	storage_bytes?: number;
	operator_judge_calls: number;
}

export interface BillingUsageReport {
	from: string;
	to: string;
	days: UsageDay[];
	totals: { shared_worker_minutes: number; runs: number; evaluations: number; operator_judge_calls: number };
	storage_bytes?: number;
	unavailable?: string[];
}

export interface Invoice {
	id: string;
	number: string;
	status: string;
	amount_due: number;
	amount_paid: number;
	currency: string;
	created: string;
	hosted_url?: string;
	pdf_url?: string;
}

export interface SeatSync {
	members: number;
	seats: number;
	changed: boolean;
}

export interface PublicPricing {
	currency: string;
	plans: Plan[];
	features: BillingFeature[];
	billing_enabled: boolean;
	contact?: string;
	notes: string[];
}
