import "@/lib/i18n";

import { render, okQuery, mutation } from "@/lib/testing/render";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { describe, expect, it, vi } from "vitest";
import { canCopyLink, DEFAULT_EXPIRY_DAYS, expiryChoices, ShareDialog } from "./shareDialog";
import { OrgShareLinks, sortLinks } from "./orgShareLinks";
import type { Share } from "@/lib/store/apis/shareApi";

const withSecret: Share = {
	id: "shr_1",
	kind: "verdict",
	target: "exp_1",
	hide_prompts: false,
	created_at: "2026-11-22T00:00:00Z",
	state: "active",
	token: "s_abc",
	path: "/s/s_abc",
};
// What a viewer gets: the server strips token, path and url.
const stripped: Share = { ...withSecret, id: "shr_2", token: undefined, path: undefined, created_at: "2026-11-23T00:00:00Z" };

vi.mock("@/lib/store/apis/shareApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/shareApi")>()),
	useListSharesQuery: () => okQuery({ shares: [stripped] }),
	useCreateShareMutation: () => mutation(),
	useRevokeShareMutation: () => mutation(),
}));

describe("share link rules (decision 7)", () => {
	it("defaults to 7 days and offers Never only to admins", () => {
		expect(DEFAULT_EXPIRY_DAYS).toBe(7);
		expect(expiryChoices(true)).toContain(0);
		expect(expiryChoices(false)).not.toContain(0);
		const member = render(<ShareDialog kind="verdict" target="exp_1" onClose={() => undefined} />, hostedSession("member"));
		expect(member).not.toContain('data-testid="share-expiry-0"');
		expect(member).toContain('data-testid="share-never-admin"');
		expect(member).toMatch(/data-testid="share-expiry-7"[^>]*aria-checked="true"|aria-checked="true"[^>]*data-testid="share-expiry-7"/);
		const admin = render(<ShareDialog kind="verdict" target="exp_1" onClose={() => undefined} />, hostedSession("admin"));
		expect(admin).toContain('data-testid="share-expiry-0"');
	});

	it("never offers Copy for a row without its secret", () => {
		expect(canCopyLink(withSecret)).toBe(true);
		expect(canCopyLink(stripped)).toBe(false);
		const viewer = render(<ShareDialog kind="verdict" target="exp_1" onClose={() => undefined} />, hostedSession("viewer"));
		expect(viewer).not.toContain('data-testid="share-copy-shr_2"');
		expect(viewer).toContain("Only people who can share can copy this link");
	});

	it("lists every link of the organization for admins only", () => {
		expect(sortLinks([withSecret, stripped]).map((s) => s.id)).toEqual(["shr_2", "shr_1"]);
		expect(render(<OrgShareLinks />, hostedSession("member"))).toBe("");
		const admin = render(<OrgShareLinks />, hostedSession("admin"));
		expect(admin).toContain('data-testid="org-share-shr_2"');
		expect(admin).toContain('data-testid="org-share-revoke-shr_2"');
		expect(admin).not.toMatch(/[–—]/);
	});
});
