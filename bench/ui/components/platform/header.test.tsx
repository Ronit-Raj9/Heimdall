import "@/lib/i18n";

import { LOCAL_SESSION, ME_OWNER, demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import { sessionFromMe } from "@/lib/session/sessionContext";
import { describe, expect, it, vi } from "vitest";
import { OrgSwitcher } from "./orgSwitcher";
import { UserMenu } from "./userMenu";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useSwitchOrgMutation: () => mutation(),
	useLogoutMutation: () => mutation(),
}));

describe("OrgSwitcher", () => {
	it("renders nothing in local mode", () => {
		expect(render(<OrgSwitcher />, LOCAL_SESSION)).toBe("");
	});
	it("offers the switcher to a member of several organizations, with the current role", () => {
		const html = render(<OrgSwitcher />, hostedSession("admin"));
		expect(html).toContain('data-testid="org-switcher"');
		expect(html).toContain("Acme Agents");
		expect(html).toContain('data-role="admin"');
	});
	it("shows the one organization as text when there is nothing to switch to", () => {
		const single = sessionFromMe({ me: { ...ME_OWNER, orgs: [ME_OWNER.orgs[0]] }, meLoading: false });
		const html = render(<OrgSwitcher />, single);
		expect(html).toContain('data-testid="org-current"');
		expect(html).not.toContain('data-testid="org-switcher"');
	});
	it("shows a demo visitor no organization picker", () => {
		expect(render(<OrgSwitcher />, demoSession())).not.toContain("org-switcher");
	});
});

describe("UserMenu", () => {
	it("shows the signed-in user's initials and nothing for local mode or demo visitors", () => {
		const html = render(<UserMenu />, hostedSession());
		expect(html).toContain('data-testid="user-menu-trigger"');
		expect(html).toContain(">AL<");
		expect(html).toContain('title="ada@acme.dev"');
		expect(render(<UserMenu />, LOCAL_SESSION)).toBe("");
		expect(render(<UserMenu />, demoSession())).toBe("");
	});
});