import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { activeNavItem, pageTitle } from "./navigation";

describe("page title", () => {
	// E4.title: /maxim sits under "Why agents fail" in the sidebar but has its own tab title.
	it("names the Maxim failures page by itself, not by its nav entry", () => {
		expect(activeNavItem("/maxim")?.to).toBe("/why-agents-fail");
		expect(pageTitle("/maxim")).toBe("Maxim failures · Heimdall");
		expect(pageTitle("/maxim/")).toBe("Maxim failures · Heimdall");
	});
	it("keeps the nav entry's name for other pages", () => {
		expect(pageTitle("/why-agents-fail")).toBe("Why agents fail · Heimdall");
		expect(pageTitle("/no-such-page-anywhere")).toBe("Heimdall");
	});
});