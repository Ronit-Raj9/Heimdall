import "@/lib/i18n";

import { INVITATION_PREVIEW, ME_OWNER, hostedSession, signedOutSession } from "@/lib/testing/platformFixtures";
import { errorQuery, loadingQuery, mutation, okQuery, render } from "@/lib/testing/render";
import { sessionFromMe } from "@/lib/session/sessionContext";
import { beforeEach, describe, expect, it, vi } from "vitest";
import InviteView from "./inviteView";

const api = vi.hoisted(() => ({ invite: {} as unknown, accept: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useGetInvitationQuery: () => api.invite,
	useAcceptInvitationMutation: () => mutation(api.accept),
	useLogoutMutation: () => mutation(),
}));

const grace = sessionFromMe({
	me: { ...ME_OWNER, user: { id: "usr_grace", email: "Grace@acme.dev", name: "Grace Hopper", created_at: "2026-11-01T00:00:00Z" } },
	meLoading: false,
});

beforeEach(() => {
	api.invite = okQuery(INVITATION_PREVIEW);
	api.accept = {};
});

describe("InviteView", () => {
	it("shows what the link grants and lets a visitor create an account or sign in, coming back here", () => {
		const html = render(<InviteView token="tok_1" />, signedOutSession());
		expect(html).toContain("grace@acme.dev is invited to join Acme Agents as a member.");
		expect(html).toContain('data-testid="invite-card"');
		expect(html).toContain('href="/signup?invitation=tok_1"');
		expect(html).toContain('href="/login?return_to=%2Finvite%2Ftok_1"');
	});

	it("offers only sign-in when the account exists, or sign-up is closed", () => {
		api.invite = okQuery({ ...INVITATION_PREVIEW, account_exists: true });
		expect(render(<InviteView token="t" />, signedOutSession())).not.toContain("invite-create-account");
		api.invite = okQuery({ ...INVITATION_PREVIEW, signup_allowed: false });
		const html = render(<InviteView token="t" />, signedOutSession());
		expect(html).not.toContain("invite-create-account");
		expect(html).toContain('data-testid="invite-signup-closed"');
	});

	it("lets the invited user accept (email compared without case)", () => {
		const html = render(<InviteView token="t" />, grace);
		expect(html).toContain('data-testid="invite-accept"');
		expect(html).toContain("Accept and join Acme Agents");
	});

	it("stops a different signed-in account from accepting", () => {
		const html = render(<InviteView token="t" />, hostedSession());
		expect(html).toContain('data-testid="invite-wrong-account"');
		expect(html).not.toContain("invite-accept");
	});

	it("says when the link is not valid, and shows the accept failure", () => {
		api.invite = errorQuery(404, "invitation not found");
		expect(render(<InviteView token="t" />, signedOutSession())).toContain('data-testid="invite-invalid"');
		api.invite = okQuery(INVITATION_PREVIEW);
		api.accept = { error: { status: 410, data: { error: { message: "invitation expired" } } } };
		expect(render(<InviteView token="t" />, grace)).toContain("invitation expired");
		api.invite = loadingQuery();
		expect(render(<InviteView token="t" />, grace)).toContain("invite-loading");
	});

	it("sends a signed-in user on a bad link home instead of offering Sign in (N146)", () => {
		api.invite = errorQuery(404, "invitation not found");
		const html = render(<InviteView token="zz-bad-token" />, grace);
		expect(html).toContain('data-testid="invite-invalid"');
		expect(html).toContain("Go to my home page");
		expect(html).toContain('href="/"');
		expect(html).toContain("You are signed in as Grace@acme.dev.");
		expect(html).not.toContain('href="/login"');
		const visitor = render(<InviteView token="zz-bad-token" />, signedOutSession());
		expect(visitor).toContain('href="/login"');
		expect(visitor).not.toContain("Go to my home page");
	});
});