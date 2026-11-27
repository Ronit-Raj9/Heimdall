import "@/lib/i18n";

import { formatBytes, formatMoney, meterPercent, meterTone, planCards } from "@/components/billing/billing.utils";
import { BILLING_LINK, notifyPlanLimit, onPlanLimit, planLimitOf } from "@/lib/billing/planLimit";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import type { BillingView, Plan } from "@/lib/types/billing";
import { describe, expect, it, vi } from "vitest";
import { BillingBody } from "./billingTab";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/billingApi", () => ({
	useGetBillingQuery: () => ({ isLoading: true }),
	useGetInvoicesQuery: () =>
		okQuery({
			invoices: [
				{
					id: "in_1",
					number: "A-0001",
					status: "paid",
					amount_due: 78,
					amount_paid: 78,
					currency: "usd",
					created: "2026-11-20T00:00:00Z",
					hosted_url: "https://pay.stripe.test/in_1",
				},
			],
		}),
	useStartCheckoutMutation: () => mutation(),
	useOpenPortalMutation: () => mutation(),
	useSyncSeatsMutation: () => mutation(),
}));

const PLANS: Plan[] = [
	{
		id: "free",
		name: "Free",
		summary: "s",
		price_usd: 0,
		price_unit: "free",
		contact: false,
		max_seats: 3,
		shared_worker_minutes: 300,
		features: [],
		own_key: true,
	},
	{
		id: "team",
		name: "Team",
		summary: "s",
		price_usd: 39,
		price_unit: "seat_month",
		contact: false,
		max_seats: 50,
		shared_worker_minutes: 3000,
		features: ["private_benchmarks", "ci", "alerts"],
		own_key: true,
	},
	{
		id: "enterprise",
		name: "Enterprise",
		summary: "s",
		price_usd: 0,
		price_unit: "contact",
		contact: true,
		max_seats: 0,
		shared_worker_minutes: 0,
		features: ["private_benchmarks"],
		own_key: true,
	},
];

function view(over: Partial<BillingView> = {}): BillingView {
	return {
		enabled: false,
		stripe_mode: "",
		checkout_ready: false,
		checkout_blocker: "billing is off on this server",
		portal_ready: false,
		plan: PLANS[0],
		status: "",
		paid_seats: 0,
		period_from: "2026-11-01",
		period_to: "2026-11-30",
		meters: [
			{
				id: "shared_worker_minutes",
				used: 320,
				limit: 300,
				unit: "minutes",
				period: "month",
				enforced: false,
				reached: true,
				available: true,
			},
			{ id: "seats", used: 2, limit: 3, unit: "seats", period: "now", enforced: false, reached: false, available: true },
			{ id: "storage_bytes", used: 2048, limit: 0, unit: "bytes", period: "now", enforced: false, reached: false, available: true },
		],
		features: [
			{ id: "private_benchmarks", included: false },
			{ id: "ci", included: false },
		],
		plans: PLANS,
		contact: "sales@heimdall.test",
		...over,
	};
}

describe("BillingBody", () => {
	it("says billing is off and shows limits as not enforced", () => {
		const html = render(<BillingBody view={view()} />, hostedSession("owner"));
		expect(html).toContain('data-testid="billing-off"');
		expect(html).toContain("nothing is enforced");
		expect(html).toContain("320 of 300");
		expect(html).toContain("not enforced while billing is off");
		expect(html).toContain("2.0 KB");
		// Billing off: no Upgrade at all, not a disabled one (M8-26).
		expect(html).not.toContain('data-testid="billing-upgrade"');
		expect(html).not.toContain('data-testid="billing-invoices"');
		expect(tagOf(html, "billing-plan-card-free")).toContain('data-current="true"');
		expect(html).toContain("mailto:sales@heimdall.test");
	});

	it("offers checkout to admins when Stripe is ready, in test mode", () => {
		const html = render(
			<BillingBody view={view({ enabled: true, stripe_mode: "test", checkout_ready: true, checkout_blocker: undefined })} />,
			hostedSession("admin"),
		);
		expect(html).toContain('data-testid="billing-test-mode"');
		expect(tagOf(html, "billing-upgrade")).not.toContain('disabled=""');
		expect(html).toContain('data-testid="billing-plan-upgrade-team"');
		expect(html).toContain("Limit reached");
	});

	it("keeps the upgrade from members", () => {
		const html = render(<BillingBody view={view({ enabled: true, checkout_ready: true })} />, hostedSession("member"));
		expect(tagOf(html, "billing-upgrade")).toContain('disabled=""');
		expect(html).not.toContain('data-testid="billing-plan-upgrade-team"');
	});

	it("shows a paid Team plan with the portal, seat sync and invoices", () => {
		const html = render(
			<BillingBody
				view={view({
					enabled: true,
					checkout_ready: true,
					portal_ready: true,
					plan: PLANS[1],
					status: "active",
					paid_seats: 4,
					period_end: "2026-12-20T00:00:00Z",
				})}
			/>,
			hostedSession("owner"),
		);
		expect(html).not.toContain('data-testid="billing-upgrade"');
		expect(html).toContain('data-testid="billing-portal"');
		expect(html).toContain('data-testid="billing-sync-seats"');
		expect(html).toContain("2026-12-20");
		expect(html).toContain("$39 per seat per month");
		expect(html).toContain('data-testid="billing-invoice-row"');
		expect(html).toContain("$78.00");
	});
});

describe("billing utils", () => {
	it("formats and measures", () => {
		expect(formatBytes(0)).toBe("0 B");
		expect(formatBytes(1536)).toBe("1.5 KB");
		expect(formatBytes(5 * 1024 ** 3)).toBe("5.0 GB");
		expect(meterPercent({ used: 150, limit: 300 })).toBe(50);
		expect(meterPercent({ used: 900, limit: 300 })).toBe(100);
		expect(meterPercent({ used: 5, limit: 0 })).toBe(0);
		expect(meterTone({ used: 100, limit: 300 })).toBe("ok");
		expect(meterTone({ used: 250, limit: 300 })).toBe("warn");
		expect(meterTone({ used: 300, limit: 300 })).toBe("full");
		expect(planCards(PLANS, "team").map((p) => p.current)).toEqual([false, true, false]);
		expect(formatMoney(78, "usd")).toBe("$78.00");
	});
});

describe("plan limit prompt", () => {
	it("reads only 402 plan_limit answers and tells listeners", () => {
		const refusal = {
			status: 402,
			data: { error: { type: "plan_limit", message: "Private benchmarks are not part of the Free plan", link: "/settings?tab=billing" } },
		};
		expect(planLimitOf(refusal)).toEqual({ message: "Private benchmarks are not part of the Free plan", link: BILLING_LINK });
		expect(planLimitOf({ status: 402, data: { error: { type: "other" } } })).toBeNull();
		expect(planLimitOf({ status: 403, data: { error: { type: "plan_limit" } } })).toBeNull();
		expect(planLimitOf({ status: 402, data: { error: { type: "plan_limit", link: "https://evil.test" } } })?.link).toBe(BILLING_LINK);
		const seen: string[] = [];
		const off = onPlanLimit((n) => seen.push(n.message));
		notifyPlanLimit(refusal);
		notifyPlanLimit({ status: 500 });
		off();
		notifyPlanLimit(refusal);
		expect(seen).toEqual(["Private benchmarks are not part of the Free plan"]);
	});
});