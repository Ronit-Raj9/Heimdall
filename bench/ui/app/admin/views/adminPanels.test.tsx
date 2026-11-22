import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { loadingQuery, mutation, okQuery, render } from "@/lib/testing/render";
import type { AdminContainer, AdminLimits, AdminOrg, AdminUsage } from "@/lib/types/admin";
import { describe, expect, it, vi } from "vitest";
import { AdminContainersPanel, AdminOrgsPanel, AdminUsagePanel, QuotaEditor, limitsSummary } from "./adminPanels";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const DEFAULTS: AdminLimits = {
	max_concurrent_runs: 4,
	max_runs_per_day: 0,
	max_spend_usd_per_day: 0,
	max_image_bytes: 4 << 30,
	allow_custom_images: false,
	shared_pool: true,
	runtime: "runsc",
};

const ORG: AdminOrg = {
	id: "org_01ronit",
	slug: "ronit",
	name: "Ronit Raj's Organization",
	public_read: false,
	members: 1,
	created_at: "2026-11-01T00:00:00Z",
	quota: DEFAULTS,
};

const q = vi.hoisted(() => ({ quota: null as unknown, usage: null as unknown, containers: null as unknown }));

vi.mock("@/lib/store/apis/adminApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/adminApi")>()),
	useListAdminOrgsQuery: () => okQuery({ orgs: [ORG] }),
	useGetOrgQuotaQuery: () => q.quota,
	useSetOrgQuotaMutation: () => mutation(),
	useGetAdminUsageQuery: () => q.usage,
	useListAdminContainersQuery: () => q.containers,
	useRemoveAdminRunnerMutation: () => mutation(),
}));

const admin = () => ({ ...hostedSession("owner"), serverAdmin: true });

describe("org limits (N9)", () => {
	it("reads 0 as no limit, per value", () => {
		const t = (key: string, o?: Record<string, unknown>) => `${key}${o ? JSON.stringify(o) : ""}`;
		expect(limitsSummary(DEFAULTS, t)).toBe('orgs.atOnce{"n":4}, orgs.perDayNone, orgs.spendNone');
		const html = render(<AdminOrgsPanel />, admin());
		expect(html).toContain("at most 4 at once, no daily run limit, no daily spend limit");
		expect(html).not.toContain("0 a day");
	});

	it("says 0 means no limit, marks each 0 field and fills Use defaults from the server", () => {
		q.quota = okQuery({ org_id: ORG.id, limits: DEFAULTS, defaults: { ...DEFAULTS, max_concurrent_runs: 6 }, persisted: true });
		const html = render(<QuotaEditor org={ORG} onClose={() => undefined} />, admin());
		expect(html).toContain("0 means no limit.");
		expect(html).not.toContain("none allowed");
		expect(html).toContain('data-testid="quota-max-per-day-none"');
		expect(html).toContain('data-testid="quota-max-spend-none"');
		expect(html).not.toContain('data-testid="quota-max-concurrent-none"');
		const resetTag = (h: string) => h.match(/<button[^>]*quota-reset[^>]*>/)?.[0] ?? "";
		expect(resetTag(html)).toContain("quota-reset");
		expect(resetTag(html)).not.toContain('disabled=""');
		q.quota = loadingQuery();
		expect(resetTag(render(<QuotaEditor org={ORG} onClose={() => undefined} />, admin()))).toContain('disabled=""');
	});
});

describe("usage (N10)", () => {
	it("names organizations, uses the server's totals and shows unknown cost and errors", () => {
		const usage: AdminUsage = {
			from: "2026-10-24",
			to: "2026-11-23",
			total_cost_usd: 0.14,
			total_tokens: 12000,
			runs: 9,
			orgs: [
				{
					org_id: "org_01ronit",
					slug: "ronit",
					name: "Ronit Raj's Organization",
					cost_usd: 0.14,
					tokens: 12000,
					runs: 7,
					unknown_cost_runs: 2,
				},
				{ org_id: "org_02demo", slug: "demo", name: "Demo", cost_usd: 0, runs: 2, error: "index locked" },
			],
		};
		q.usage = okQuery(usage);
		const html = render(<AdminUsagePanel />, admin());
		expect(html).toContain("Ronit Raj&#x27;s Organization");
		expect(html).toContain('title="org_01ronit"');
		expect(html).not.toMatch(/>org_01ronit</);
		expect(html).toContain('data-testid="admin-usage-runs">9<');
		expect(html).toContain("Runs with unknown cost");
		expect(html).toContain("Could not read this organization&#x27;s runs: index locked");
		expect(html).toContain("$0.14");
	});
});

describe("workers (N11)", () => {
	it("offers Remove only on offline rows and names the organization", () => {
		const row = (over: Partial<AdminContainer>): AdminContainer => ({
			org_id: "org_demo",
			org_slug: "demo",
			org_name: "Demo Org",
			runner_id: "rnr_x",
			runner: "demo-runner",
			pool: "default",
			host: "h",
			state: "lost",
			live: false,
			slots: 1,
			running: [],
			last_seen: "2026-11-22T02:55:00Z",
			...over,
		});
		q.containers = okQuery({
			runners: [row({}), row({ runner_id: "rnr_live", runner: "shared-runner-org_demo", live: true, state: "idle" })],
			running_slots: 0,
		});
		const html = render(<AdminContainersPanel />, admin());
		expect(html).toContain('data-testid="admin-runner-remove-rnr_x"');
		expect(html).not.toContain('data-testid="admin-runner-remove-rnr_live"');
		expect(html).toContain("Demo Org");
	});
});