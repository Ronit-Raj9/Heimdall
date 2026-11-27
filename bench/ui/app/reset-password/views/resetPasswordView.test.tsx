import "@/lib/i18n";

import { MembersTab } from "@/components/settings/membersTab";
import { hostedSession, signedOutSession } from "@/lib/testing/platformFixtures";
import { errorQuery, loadingQuery, okQuery, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ResetPasswordView from "./resetPasswordView";

const api = vi.hoisted(() => ({ reset: {} as unknown, resetPw: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/accountSafetyApi", async () => {
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		useGetResetQuery: () => api.reset,
		useResetPasswordMutation: () => m(api.resetPw),
		useCreateResetLinkMutation: () => m({}),
	};
});
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/platformApi")>();
	const { mutation: m, okQuery: ok } = await import("@/lib/testing/render");
	const fx = await import("@/lib/testing/platformFixtures");
	return {
		...real,
		useListMembersQuery: () => ok(fx.MEMBERS),
		useListInvitationsQuery: () => ok(fx.INVITATIONS),
		useUpdateMemberMutation: () => m({}),
		useRemoveMemberMutation: () => m({}),
		useCreateInvitationMutation: () => m({}),
		useRevokeInvitationMutation: () => m({}),
	};
});

beforeEach(() => {
	api.reset = okQuery({ email: "alan@acme.dev", expires_at: "2099-01-01T00:00:00Z" });
	api.resetPw = {};
});

describe("ResetPasswordView", () => {
	it("asks for the new password twice for the account the link resets", () => {
		const html = render(<ResetPasswordView token="tok" />, signedOutSession());
		expect(html).toContain('data-testid="reset-form"');
		expect(html).toContain("alan@acme.dev");
		expect(html).toContain('data-testid="reset-repeat"');
	});
	it("explains a missing token and a dead link, with a way forward", () => {
		expect(render(<ResetPasswordView />, signedOutSession())).toContain('data-testid="reset-missing"');
		api.reset = errorQuery(404, "reset link not found");
		const html = render(<ResetPasswordView token="t" />, signedOutSession());
		expect(html).toContain('data-testid="reset-invalid"');
		expect(html).toContain("Ask your owner for a new link.");
		expect(html).toContain('data-testid="reset-sign-in"');
	});
	it("shows loading, then the server's refusal", () => {
		api.reset = loadingQuery();
		expect(render(<ResetPasswordView token="t" />, signedOutSession())).toContain('data-testid="reset-loading"');
		api.reset = okQuery({ email: "a@b.dev", expires_at: "2099-01-01T00:00:00Z" });
		api.resetPw = { error: { status: 400, data: { error: { message: "password is too common" } } } };
		expect(render(<ResetPasswordView token="t" />, signedOutSession())).toContain('data-testid="reset-error"');
	});
});

describe("MembersTab reset links", () => {
	it("offers owners a reset link for others, never themselves, and not to admins", () => {
		const org = hostedSession().org!;
		const owner = render(<MembersTab org={org} />, hostedSession("owner"));
		expect(owner).toContain('data-testid="member-reset-usr_grace"');
		expect(owner).not.toContain("member-reset-usr_ada");
		const admin = render(<MembersTab org={org} />, hostedSession("admin"));
		expect(admin).not.toContain("member-reset-");
	});
});
