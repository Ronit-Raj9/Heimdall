import "@/lib/i18n";

import { render, okQuery, mutation } from "@/lib/testing/render";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { describe, expect, it, vi } from "vitest";
import { labelTaken } from "./shareDialog";
import { OrgShareLinks } from "./orgShareLinks";
import type { Share } from "@/lib/store/apis/shareApi";

const long: Share = {
	id: "shr_0123456789abcdef0123456789",
	kind: "evaluation",
	target: "eval_0123456789abcdef0123456789abcdef",
	hide_prompts: false,
	created_at: "2026-11-22T00:00:00Z",
	expires_at: "2026-11-29T00:00:00Z",
	state: "active",
};

vi.mock("@/lib/store/apis/shareApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/shareApi")>()),
	useListSharesQuery: () => okQuery({ shares: [long] }),
	useCreateShareMutation: () => mutation(),
	useRevokeShareMutation: () => mutation(),
}));

describe("Settings, Shared links table (Q-23, Q-281)", () => {
	it("fits every column at desktop width and stacks into cards on phones", () => {
		const html = render(<OrgShareLinks />, hostedSession("admin"));
		// Fixed layout with set column widths: Expires, State and Turn off stay inside the table.
		expect(html).toMatch(/data-stack=""[^>]*class="[^"]*sm:table-fixed/);
		expect(html).not.toContain("overflow-x-auto rounded-lg border");
		// Long ids are cut with the full id on hover.
		expect(html).toContain(`title="${long.id}"`);
		expect(html).toContain(`title="${long.target}"`);
		// A stacked row names each value.
		expect(html).toContain('data-label="Expires"');
		expect(html).toContain('data-testid="org-share-revoke-shr_0123456789abcdef0123456789"');
	});
});

describe("share label hint (Q-283)", () => {
	it("flags a label an active link of this view already uses", () => {
		const a = { ...long, label: "For Priya" };
		expect(labelTaken([a], " for priya ")).toBe(true);
		expect(labelTaken([{ ...a, state: "revoked" }], "For Priya")).toBe(false);
		expect(labelTaken([a], "")).toBe(false);
	});
});
