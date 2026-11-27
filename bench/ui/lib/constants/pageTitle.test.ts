import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { activeNavItem, pageTitle } from "./navigation";

describe("page title", () => {
	// Spec 1: /maxim is the Production failures list filtered to one source.
	it("names the Maxim failures page as Production failures, under its entry", () => {
		expect(activeNavItem("/maxim")?.to).toBe("/inbox");
		expect(pageTitle("/maxim")).toBe("Production failures · Heimdall");
		expect(pageTitle("/maxim/")).toBe("Production failures · Heimdall");
	});
	it("keeps the nav entry's name for other pages", () => {
		expect(pageTitle("/why-agents-fail")).toBe("Failures · Heimdall");
		expect(pageTitle("/no-such-page-anywhere")).toBe("Heimdall");
	});
});