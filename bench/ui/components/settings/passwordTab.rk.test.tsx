import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { PasswordTab } from "./passwordTab";

vi.mock("@/lib/store/apis/platformApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/platformApi")>()),
	useChangePasswordMutation: () => mutation(),
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("PasswordTab (Q-236)", () => {
	it("asks a GitHub-only account for no current password", () => {
		const html = render(<PasswordTab />, { ...hostedSession("member"), hasPassword: false });
		expect(html).toContain("Set a password");
		expect(html).not.toContain('data-testid="password-current"');
	});
	it("keeps the current password field otherwise, also when unknown", () => {
		expect(render(<PasswordTab />, { ...hostedSession("member"), hasPassword: true })).toContain('data-testid="password-current"');
		expect(render(<PasswordTab />, hostedSession("member"))).toContain('data-testid="password-current"');
	});
});
