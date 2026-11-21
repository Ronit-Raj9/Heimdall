import "@/lib/i18n";

import { LOCAL_SESSION, hostedSession } from "@/lib/testing/platformFixtures";
import { errorQuery, mutation, okQuery, render } from "@/lib/testing/render";
import type { AdminOrg } from "@/lib/types/admin";
import { describe, expect, it, vi } from "vitest";
import { setQuotaRequest } from "@/lib/store/apis/adminApi";
import AdminView, { adminTab } from "./adminView";
import { AdminImagesPanel, AdminOrgsPanel, QuotaEditor } from "./adminPanels";

const tab = vi.hoisted(() => ({ value: null as string | null }));
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: () => [tab.value, () => undefined],
}));

const ORG: AdminOrg = {
	id: "org_acme",
	slug: "acme",
	name: "Acme",
	public_read: false,
	members: 3,
	created_at: "2026-11-01T00:00:00Z",
	quota: {
		max_concurrent_runs: 4,
		max_runs_per_day: 40,
		max_spend_usd_per_day: 12,
		max_image_bytes: 2 << 30,
		allow_custom_images: true,
		shared_pool: true,
		runtime: "runsc",
	},
};

vi.mock("@/lib/store/apis/adminApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/adminApi")>()),
	useGetAdminOverviewQuery: () => okQuery({ users: 7, orgs: 2, running_slots: 1, kill_switch: true, quota_persisted: false }),
	useSetKillSwitchMutation: () => mutation(),
	useListAdminOrgsQuery: () => okQuery({ orgs: [ORG] }),
	useSetOrgQuotaMutation: () => mutation(),
	useListAdminImagesQuery: () => errorQuery(404, "no route"),
	useDecideImageMutation: () => mutation(),
}));

const admin = () => ({ ...hostedSession("owner"), serverAdmin: true });

describe("AdminView", () => {
	it("refuses a signed-in user who is not a server admin", () => {
		const html = render(<AdminView />, hostedSession("owner"));
		expect(html).toContain('data-testid="admin-forbidden"');
		expect(html).toContain("HEIMDALL_SERVER_ADMINS");
		expect(html).not.toContain('data-testid="admin-view"');
	});

	it("explains local mode", () => {
		expect(render(<AdminView />, LOCAL_SESSION)).toContain('data-testid="admin-local"');
	});

	it("shows totals, the kill switch and the stub quota warning to a server admin", () => {
		tab.value = null;
		const html = render(<AdminView />, admin());
		expect(html).toContain('data-testid="admin-view"');
		expect(html).toContain('data-testid="admin-kill-switch"');
		expect(html).toContain("Resume runs");
		expect(html).toContain('data-testid="admin-quota-stub"');
		for (const key of ["users", "orgs", "images", "usage", "containers"]) expect(html).toContain(`data-testid="admin-tab-${key}"`);
	});

	it("lists orgs with their quota and edits it", () => {
		const html = render(<AdminOrgsPanel />, admin());
		expect(html).toContain('data-testid="admin-org-acme"');
		expect(html).toContain("4 at once, 40 a day, $12 a day");
		expect(html).toContain("gVisor");
		const editor = render(<QuotaEditor org={ORG} onClose={() => undefined} />, admin());
		expect(editor).toContain('data-testid="quota-save"');
		expect(editor).toMatch(/data-testid="quota-max-concurrent"[^>]*value="4"|value="4"[^>]*data-testid="quota-max-concurrent"/);
	});

	it("says image approval is not enabled when the route is missing", () => {
		expect(render(<AdminImagesPanel />, admin())).toContain('data-testid="admin-images-unavailable"');
	});

	it("builds the quota request and parses tabs", () => {
		expect(setQuotaRequest("org/x", ORG.quota!)).toMatchObject({ url: "/admin/orgs/org%2Fx/quota", method: "POST" });
		expect(adminTab("orgs")).toBe("orgs");
		expect(adminTab("nope")).toBe("overview");
	});
});
