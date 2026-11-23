import "@/lib/i18n";

import { mutation } from "@/lib/testing/render";
import type { SignupSettings } from "@/lib/types/identity";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { requesterName } from "./adminPanels";
import { SignupForm, updaterName } from "./adminSignupPanel";

// Fix wave 3, L3: platform admin pages show names, with the email as a tooltip, never a usr_ id.

vi.mock("@/lib/store/apis/identityApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/identityApi")>()),
	useSetSignupSettingsMutation: () => mutation(),
}));

const settings = {
	mode: "invite",
	domains: [],
	source: "admin",
	config_mode: "open",
	config_domains: [],
	updated_by: "usr_01abc",
	updated_by_name: "Root Admin",
	updated_by_email: "root@acme.com",
	updated_at: "2026-11-20T10:00:00Z",
	email_delivery: "log",
} as unknown as SignupSettings;

describe("admin names", () => {
	it("names the admin who changed signup, email as the title", () => {
		const html = renderToStaticMarkup(<SignupForm settings={settings} />);
		expect(html).toContain("Root Admin");
		expect(html).toContain('title="root@acme.com"');
		expect(html).not.toContain("usr_01abc");
	});

	it("never prints a raw user id", () => {
		expect(updaterName({ ...settings, updated_by_name: undefined } as SignupSettings)).toBe("");
		expect(requesterName({ requested_by: "usr_01abc", requested_by_name: "Bo" })).toBe("Bo");
		expect(requesterName({ requested_by: "usr_01abc" })).toBe("");
		expect(requesterName({ requested_by: "bo@acme.com" })).toBe("bo@acme.com");
	});
});