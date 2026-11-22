import "@/lib/i18n";

import { LOCAL_SESSION, ME_OWNER, demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import { sessionFromMe } from "@/lib/session/sessionContext";
import { describe, expect, it, vi } from "vitest";
import { createOrgRequest } from "@/lib/store/apis/platformApi";
import { CreateOrgDialog, slugFromName } from "./createOrgDialog";
import { HelpMenu } from "./helpMenu";
import { OrgSwitcher } from "./orgSwitcher";
import { UserMenu } from "./userMenu";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useSwitchOrgMutation: () => mutation(),
	useCreateOrgMutation: () => mutation(),
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
	// J1: a member of one organization still gets a menu, with "Create organization".
	it("makes the badge a menu with Create organization for a member of one organization", () => {
		const single = sessionFromMe({ me: { ...ME_OWNER, orgs: [ME_OWNER.orgs[0]] }, meLoading: false });
		const closed = render(<OrgSwitcher />, single);
		expect(closed).toContain('data-testid="org-switcher"');
		expect(closed).toContain('aria-haspopup="menu"');
		expect(closed).not.toContain('data-testid="org-current"');
		const open = render(<OrgSwitcher defaultOpen />, single);
		expect(open).toContain('role="menu"');
		expect(open).toContain('data-testid="org-option-acme"');
		expect(open).toMatch(/role="menuitemradio"[^>]*aria-checked="true"/);
		expect(open).toContain('data-testid="org-create"');
		expect(open).toContain("Create organization");
	});
	it("lists every organization in the open menu, the current one checked", () => {
		const open = render(<OrgSwitcher defaultOpen />, hostedSession("owner"));
		expect(open).toContain("Acme Agents");
		expect(open).toContain("Acme Labs");
		expect(open.match(/aria-checked="true"/g)).toHaveLength(1);
		expect(open).toContain('data-testid="org-create"');
	});
	it("shows a demo visitor no organization picker", () => {
		expect(render(<OrgSwitcher />, demoSession())).not.toContain("org-switcher");
	});
	// Audit qc2-17-02: after leaving the current org the session has memberships but no current
	// org; the picker is offered, and the remaining membership is not shown as the current one.
	for (const orgs of [[ME_OWNER.orgs[1]], ME_OWNER.orgs]) {
		it(`offers the picker with ${orgs.length} membership(s) and no current org`, () => {
			const s = sessionFromMe({ me: { ...ME_OWNER, orgs, current_org: undefined, role: undefined }, meLoading: false });
			expect(s.org).toBeUndefined();
			const html = render(<OrgSwitcher />, s);
			expect(html).toContain('data-testid="org-switcher"');
			expect(html).toContain("Choose an organization");
			expect(html).not.toContain('data-testid="org-current"');
		});
	}
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
describe("CreateOrgDialog", () => {
	it("asks for a name and offers Create and Cancel", () => {
		const html = render(<CreateOrgDialog onClose={() => {}} />, hostedSession());
		expect(html).toContain('role="dialog"');
		expect(html).toContain('data-testid="create-org-name"');
		expect(html).toContain('data-testid="create-org-submit"');
		expect(html).toContain('data-testid="create-org-cancel"');
		expect(html).toContain(">Create<");
		expect(html).toContain(">Cancel<");
	});
	it("derives a valid short id from the name", () => {
		expect(slugFromName("Ronit's Research Lab")).toBe("ronit-s-research-lab");
		expect(slugFromName("  --Acme__Agents--  ")).toBe("acme-agents");
		expect(slugFromName("x".repeat(60))).toHaveLength(40);
		expect(slugFromName("A")).toBe("a");
	});
	it("sends POST /orgs with the name and short id", () => {
		expect(createOrgRequest({ name: " Acme ", slug: "acme" })).toEqual({
			url: "/orgs",
			method: "POST",
			body: { name: "Acme", slug: "acme" },
		});
	});
});

describe("HelpMenu", () => {
	it("is a menu button that keeps the header-help test id", () => {
		const html = render(<HelpMenu />, hostedSession());
		expect(html).toContain('data-testid="header-help"');
		expect(html).toContain('aria-haspopup="menu"');
	});
	it("offers guides, glossary, quickstart and system status", () => {
		const html = render(<HelpMenu defaultOpen />, hostedSession());
		expect(html).toContain('role="menu"');
		expect(html.match(/role="menuitem"/g)).toHaveLength(4);
		expect(html).toContain('href="/docs"');
		expect(html).toContain('href="/docs?doc=glossary"');
		expect(html).toContain('href="/quickstart"');
		expect(html).toContain('href="/status"');
		for (const label of ["Guides", "Glossary", "Quickstart", "System status"]) expect(html).toContain(label);
	});
});