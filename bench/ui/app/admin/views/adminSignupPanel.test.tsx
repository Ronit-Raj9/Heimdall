import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { errorQuery, mutation, okQuery, render } from "@/lib/testing/render";
import type { SignupSettings } from "@/lib/types/identity";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AdminSignupPanel, SignupForm, parseDomains } from "./adminSignupPanel";

const api = vi.hoisted(() => ({ settings: {} as unknown }));
vi.mock("@/lib/store/apis/identityApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/identityApi")>()),
	useGetSignupSettingsQuery: () => api.settings,
	useSetSignupSettingsMutation: () => mutation(),
}));

const CONFIG: SignupSettings = {
	mode: "invite",
	domains: [],
	source: "config",
	config_mode: "invite",
	config_domains: [],
	email_delivery: "log",
};

const admin = () => ({ ...hostedSession("owner"), serverAdmin: true });

beforeEach(() => {
	api.settings = okQuery(CONFIG);
});

describe("AdminSignupPanel", () => {
	it("shows the four modes with the current one checked, from the server config", () => {
		const html = render(<AdminSignupPanel />, admin());
		for (const m of ["invite", "allowlist", "open", "closed"]) expect(html).toContain(`data-testid="admin-signup-mode-${m}"`);
		expect(html).toMatch(/aria-checked="true"[^>]*data-testid="admin-signup-mode-invite"/);
		expect(html).toContain("HEIMDALL_SIGNUP");
		expect(html).not.toContain('data-testid="admin-signup-reset"');
		expect(html).toContain("verification links go to the server log");
	});

	it("shows the allowed domains and who changed the setting", () => {
		const html = render(
			<SignupForm
				settings={{ ...CONFIG, mode: "allowlist", domains: ["acme.dev"], source: "admin", updated_by: "usr_ada", updated_by_name: "Ada", updated_at: "2026-11-20T09:00:00Z", email_delivery: "email" } as SignupSettings}
			/>,
			admin(),
		);
		expect(html).toContain('data-testid="admin-signup-domains"');
		expect(html).toContain("acme.dev");
		expect(html).toContain("Set here by Ada"); // fix wave 3, L3: the name, never the user id
		expect(html).not.toContain("usr_ada");
		expect(html).toContain('data-testid="admin-signup-reset"');
		expect(html).toContain("Verification links are emailed.");
	});

	it("explains a failed load", () => {
		api.settings = errorQuery(403, "server admins only");
		expect(render(<AdminSignupPanel />, admin())).toContain('data-testid="admin-signup-error"');
	});

	it("parses the domains box", () => {
		expect(parseDomains(" Acme.dev,\n@labs.dev  acme.dev\n\n")).toEqual(["acme.dev", "labs.dev"]);
		expect(parseDomains("")).toEqual([]);
	});
});
