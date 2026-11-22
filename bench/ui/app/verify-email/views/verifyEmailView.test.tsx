import "@/lib/i18n";

import { hostedSession, signedOutSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import VerifyEmailView from "./verifyEmailView";

const api = vi.hoisted(() => ({ verify: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/identityApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/identityApi")>()),
	useVerifyEmailMutation: () => mutation(api.verify),
}));

beforeEach(() => {
	api.verify = {};
});

describe("VerifyEmailView", () => {
	it("asks for a press before it verifies (a mail scanner fetching the link verifies nothing)", () => {
		const html = render(<VerifyEmailView token="tok" />, signedOutSession());
		expect(html).toContain('data-testid="verify-screen"');
		expect(html).toContain('data-testid="verify-submit"');
		expect(html).toContain("Confirm email");
		expect(html).toContain("expires 24 hours");
	});

	it("explains a link without a token", () => {
		const html = render(<VerifyEmailView />, signedOutSession());
		expect(html).toContain('data-testid="verify-missing"');
		expect(html).toContain('data-testid="verify-sign-in"');
	});

	it("explains a used or expired link, and sends signed-in users back to the app", () => {
		api.verify = { isError: true, error: { status: 404, data: { error: { message: "gone" } } } };
		const html = render(<VerifyEmailView token="tok" />, hostedSession("owner"));
		expect(html).toContain('data-testid="verify-invalid"');
		expect(html).toContain('data-testid="verify-continue"');
	});

	it("shows other failures with the server's message", () => {
		api.verify = { isError: true, error: { status: 429, data: { error: { message: "too many attempts; try again later" } } } };
		const html = render(<VerifyEmailView token="tok" />, signedOutSession());
		expect(html).toContain('data-testid="verify-error"');
		expect(html).toContain("too many attempts");
	});
});
