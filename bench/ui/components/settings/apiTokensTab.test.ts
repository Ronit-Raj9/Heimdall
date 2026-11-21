import { describe, expect, it } from "vitest";
import { PUBLIC_API_BASE, tokenScope, tokenUsage } from "./apiTokensTab";

describe("API token scope", () => {
	it("shows the widest scope; tokens without scopes act on their role alone", () => {
		expect(tokenScope({ scopes: ["read", "evaluate"] })).toBe("evaluate");
		expect(tokenScope({ scopes: ["read"] })).toBe("read");
		expect(tokenScope({ scopes: ["admin"] })).toBe("admin");
		expect(tokenScope({})).toBe("admin");
	});

	it("points the example request at the public API", () => {
		expect(PUBLIC_API_BASE).toBe("/api/v1");
		expect(tokenUsage("https://h.dev", "hm_x")).toBe('curl -H "Authorization: Bearer hm_x" https://h.dev/api/v1/evaluations');
	});
});
