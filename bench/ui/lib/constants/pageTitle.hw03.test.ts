import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { pageTitle } from "./navigation";

// Fix wave 3, N255: the Safety monitors page has its own tab title, not its nav entry's "Settings".
describe("pageTitle /monitors", () => {
	it("reads Safety monitor tests", () => {
		expect(pageTitle("/monitors")).toBe("Safety monitor tests · Heimdall");
	});
});