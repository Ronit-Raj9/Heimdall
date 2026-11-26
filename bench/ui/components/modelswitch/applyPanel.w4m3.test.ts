import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { applyMissing } from "./applyPanel";

describe("applyMissing (p06-4)", () => {
	it("names what is missing", () => {
		expect(applyMissing("", "virtual_key", "", false)).toBe("url");
		expect(applyMissing("https://gw", "virtual_key", "", false)).toBe("scopeId");
		expect(applyMissing("https://gw", "global", "", false)).toBe("global");
		expect(applyMissing("https://gw", "team", "t1", false)).toBeNull();
	});
});