import "@/lib/i18n";

import { hostedSession, INVITATIONS, MEMBERS } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { MembersTab } from "./membersTab";

// Fix wave 3, N223: revoking a pending invitation asks first; nothing is sent until the person
// confirms.

const sent = vi.hoisted(() => ({
	revoke: [] as unknown[],
	buttons: [] as { testId: string; question: string; confirmLabel: string; onConfirm: () => void }[],
}));

vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useListMembersQuery: () => okQuery(MEMBERS),
	useListInvitationsQuery: () => okQuery(INVITATIONS),
	useCreateInvitationMutation: () => mutation(),
	useUpdateMemberMutation: () => mutation(),
	useRemoveMemberMutation: () => mutation(),
	useRevokeInvitationMutation: () => [
		(arg: unknown) => sent.revoke.push(arg),
		{ isLoading: false, error: undefined, originalArgs: undefined },
	],
}));
vi.mock("@/lib/store/apis/accountSafetyApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/accountSafetyApi")>()),
	useCreateResetLinkMutation: () => mutation(),
}));
vi.mock("@/components/platform/badges", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/components/platform/badges")>()),
	ConfirmButton: (p: { testId: string; question: string; confirmLabel: string; onConfirm: () => void; label: string }) => {
		sent.buttons.push(p);
		return <button data-testid={p.testId}>{p.label}</button>;
	},
}));

const ORG = { id: "org_acme", slug: "acme", name: "Acme Agents", role: "owner" as const };

describe("MembersTab invitations", () => {
	it("Revoke asks first and sends nothing until confirmed", () => {
		const html = render(<MembersTab org={ORG} />, hostedSession("owner"));
		expect(html).toContain('data-testid="invitation-revoke-inv_1"');
		const revoke = sent.buttons.find((b) => b.testId === "invitation-revoke-inv_1");
		expect(revoke?.question).toBe("Revoke the invitation for edsger@acme.dev? The link stops working at once.");
		expect(revoke?.confirmLabel).toBe("Revoke invitation");
		expect(sent.revoke).toEqual([]);
		revoke?.onConfirm();
		expect(sent.revoke).toEqual([{ orgId: "org_acme", invitationId: "inv_1" }]);
	});
});