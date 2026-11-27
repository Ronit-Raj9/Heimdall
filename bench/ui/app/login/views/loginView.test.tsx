import "@/lib/i18n";

import { LOCAL_SESSION, signedOutSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import LoginView from "./loginView";

const api = vi.hoisted(() => ({ login: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useLoginMutation: () => mutation(api.login),
}));

beforeEach(() => {
	api.login = {};
});

describe("LoginView", () => {
	it("asks for email and password, offers GitHub with the return path, and sign-up when open", () => {
		const html = render(<LoginView returnTo="/verdict?exp=e_1" />, signedOutSession());
		expect(html).toContain('data-testid="login-form"');
		expect(html).toContain('data-testid="login-email"');
		expect(html).toContain('type="password"');
		expect(html).toContain('href="/api/bench/auth/github/start?return_to=%2Fverdict%3Fexp%3De_1"');
		expect(html).toContain('data-testid="login-signup-link"');
		expect(html).not.toContain("write token");
	});

	it("leaves out GitHub and sign-up when the server does not offer them, and never returns off-site", () => {
		const html = render(
			<LoginView returnTo="https://evil.example" />,
			signedOutSession({ mode: "live", auth_mode: "hosted", signup: "invite", github: false }),
		);
		expect(html).not.toContain("auth-github");
		expect(html).not.toContain("login-signup-link");
	});

	it("links to sign-up under invite-only when the install is empty (bootstrap)", () => {
		const cfg = { mode: "live", auth_mode: "hosted", signup: "invite", github: false } as const;
		expect(render(<LoginView />, signedOutSession({ ...cfg, bootstrap: true }))).toContain('data-testid="login-signup-link"');
		expect(render(<LoginView />, signedOutSession({ ...cfg, bootstrap: false }))).not.toContain("login-signup-link");
	});

	it("says wrong credentials and rate limits in plain words", () => {
		api.login = { error: { status: 401, data: { error: { message: "invalid credentials" } } } };
		expect(render(<LoginView />, signedOutSession())).toContain("That email and password do not match an account.");
		api.login = { error: { status: 429, data: { error: { message: "slow down" } } } };
		expect(render(<LoginView />, signedOutSession())).toContain("Too many attempts.");
	});

	it("says the session ended when the redirect carries reason expired", () => {
		const html = render(<LoginView returnTo="/runs" expired />, signedOutSession());
		expect(html).toContain('data-testid="login-expired"');
		expect(html).toContain("Your session ended. Sign in again to go back to /runs.");
		expect(render(<LoginView returnTo="/runs" />, signedOutSession())).not.toContain("login-expired");
	});

	it("explains a GitHub sign-in that did not complete", () => {
		expect(render(<LoginView oauthError="github" />, signedOutSession())).toContain("GitHub sign-in did not complete.");
		expect(render(<LoginView oauthError="signup_not_allowed" />, signedOutSession())).toContain("sign-up is not open here");
	});

	it("tells a local-mode visitor there is nothing to sign in to", () => {
		const html = render(<LoginView />, LOCAL_SESSION);
		expect(html).toContain('data-testid="login-local"');
		expect(html).toContain("Local server, no sign-in needed.");
		expect(html).not.toContain("login-form");
	});
});
describe("LoginView layout", () => {
	it("leads with GitHub above the email form and shows no judge hint", () => {
		const html = render(<LoginView />, signedOutSession());
		expect(html.indexOf('data-testid="auth-github"')).toBeLessThan(html.indexOf('data-testid="login-form"'));
		expect(html).not.toContain('data-testid="login-judge-hint"');
		expect(html).not.toMatch(/judg/i);
		expect(html).toContain("or sign in with email");
	});
});
