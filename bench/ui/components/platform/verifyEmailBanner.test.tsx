import "@/lib/i18n";

import { LOCAL_SESSION, hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { VerifyEmailBanner } from "./verifyEmailBanner";

const api = vi.hoisted(() => ({ resend: {} as Record<string, unknown>, config: { data: undefined } as unknown }));
vi.mock("@/lib/store/apis/identityApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/identityApi")>()),
	useResendVerificationMutation: () => mutation(api.resend),
}));
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useGetAuthConfigQuery: () => api.config,
}));

const unverified = () => ({ ...hostedSession("owner"), emailVerified: false });

beforeEach(() => {
	api.resend = {};
	api.config = { data: undefined };
});

describe("VerifyEmailBanner", () => {
	it("is hidden for verified users, older servers and local mode", () => {
		expect(render(<VerifyEmailBanner />, { ...hostedSession("owner"), emailVerified: true })).toBe("");
		expect(render(<VerifyEmailBanner />, hostedSession("owner"))).toBe("");
		expect(render(<VerifyEmailBanner />, LOCAL_SESSION)).toBe("");
	});

	it("says what stays locked and offers a new link", () => {
		const html = render(<VerifyEmailBanner />, unverified());
		expect(html).toContain('data-testid="verify-banner"');
		expect(html).toContain("Link sent to ada@acme.dev");
		expect(html).toContain("not create API tokens, invite people or create organizations");
		expect(html).toContain('data-testid="verify-resend"');
	});

	it("points at the server admin when the server sends no email", () => {
		api.config = okQuery({ auth_mode: "hosted", github: false, email_delivery: "log" });
		const html = render(<VerifyEmailBanner />, unverified());
		expect(html).toContain('data-delivery="log"');
		expect(html).toContain("Ask your admin for the link");
	});

	it("confirms a resend", () => {
		api.resend = { data: { email: "ada@acme.dev", delivery: "email" } };
		expect(render(<VerifyEmailBanner />, unverified())).toContain("A new link is on its way to ada@acme.dev.");
		api.resend = { data: { email: "ada@acme.dev", delivery: "log" } };
		expect(render(<VerifyEmailBanner />, unverified())).toContain("A new link is in the server log.");
	});
});
