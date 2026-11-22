import { describe, expect, it } from "vitest";
import { retryBody } from "./fromIssues";

describe("retryBody", () => {
	it("builds the fix body or null", () => {
		expect(retryBody("retry", null)).toEqual({ fix: "retry" });
		expect(retryBody("add_secret", " API_TOKEN ")).toEqual({ fix: "add_secret", secrets: ["API_TOKEN"] });
		expect(retryBody("allow_service", "postgres")).toEqual({ fix: "allow_service", allow_services: ["postgres"] });
		expect(retryBody("raise_budget", "5")).toEqual({ fix: "raise_budget", budget_usd: 5 });
		expect(retryBody("raise_budget", "x")).toBeNull();
		expect(retryBody("add_secret", null)).toBeNull();
	});
});
