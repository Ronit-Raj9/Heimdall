import "@/lib/i18n";

import { AUTH_CONFIG, INVITATION_PREVIEW, signedOutSession } from "@/lib/testing/platformFixtures";
import { errorQuery, loadingQuery, mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SignupView from "./signupView";

const api = vi.hoisted(() => ({ invite: {} as unknown, signup: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useGetInvitationQuery: () => api.invite,
	useSignupMutation: () => mutation(api.signup),
}));

beforeEach(() => {
	api.invite = { ...loadingQuery(), isLoading: false };
	api.signup = {};
});

describe("SignupView", () => {
	it("shows the form when sign-up is open", () => {
		const html = render(<SignupView />, signedOutSession());
		expect(html).toContain('data-testid="signup-form"');
		expect(html).toContain("Your first organization is created with you as its owner.");
		expect(html).toContain('data-testid="auth-github"');
	});

	it("says invite-only and closed servers sign people up another way", () => {
		expect(render(<SignupView />, signedOutSession({ ...AUTH_CONFIG, signup: "invite" }))).toContain('data-testid="signup-invite-only"');
		expect(render(<SignupView />, signedOutSession({ ...AUTH_CONFIG, signup: "closed" }))).toContain('data-testid="signup-closed"');
	});

	it("joins the inviting organization with the invited email filled in", () => {
		api.invite = okQuery(INVITATION_PREVIEW);
		const html = render(<SignupView invitation="tok" />, signedOutSession({ ...AUTH_CONFIG, signup: "invite" }));
		expect(html).toContain("Join Acme Agents as a member.");
		expect(html).toContain('data-testid="signup-invite-org"');
		expect(tagOf(html, "signup-email")).toContain('value="grace@acme.dev"');
		expect(tagOf(html, "signup-email")).toContain("readOnly");
		expect(html).not.toContain("auth-github");
	});

	it("says when an invitation link is no longer valid", () => {
		api.invite = errorQuery(404, "invitation not found");
		expect(render(<SignupView invitation="tok" />, signedOutSession())).toContain('data-testid="signup-invite-invalid"');
	});

	it("shows the server's reason when sign-up fails", () => {
		api.signup = { error: { status: 409, data: { error: { message: "an account with this email exists" } } } };
		expect(render(<SignupView />, signedOutSession())).toContain("an account with this email exists");
	});
});