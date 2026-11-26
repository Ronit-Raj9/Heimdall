import "@/lib/i18n";

import { hostedSession, INVITATIONS, MEMBERS } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { MembersTab } from "./membersTab";

// Wave 4, N33: only admins see the members' email addresses on Members.

vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useListMembersQuery: () => okQuery(MEMBERS),
	useListInvitationsQuery: () => okQuery(INVITATIONS),
	useCreateInvitationMutation: () => mutation(),
	useUpdateMemberMutation: () => mutation(),
	useRemoveMemberMutation: () => mutation(),
	useRevokeInvitationMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/accountSafetyApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/accountSafetyApi")>()),
	useCreateResetLinkMutation: () => mutation(),
}));

const ORG = { id: "org_acme", slug: "acme", name: "Acme Agents", role: "member" as const };

describe("MembersTab emails", () => {
	it("a member sees no email column", () => {
		const html = render(<MembersTab org={ORG} />, hostedSession("member"));
		expect(html).toContain('data-testid="members-table"');
		expect(html).not.toContain("@acme.dev");
		expect(html).not.toContain(">Email<");
		expect(html).toContain("Grace Hopper");
		expect(html).toContain("Someone");
	});

	it("an admin still sees the addresses", () => {
		const html = render(<MembersTab org={{ ...ORG, role: "admin" }} />, hostedSession("admin"));
		expect(html).toContain("grace@acme.dev");
		expect(html).toContain("alan@acme.dev");
	});
});