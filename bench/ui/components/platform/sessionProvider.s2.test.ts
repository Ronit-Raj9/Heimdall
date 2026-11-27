import { describe, expect, it } from "vitest";
import { loginSearch, markSignedIn } from "@/components/platform/sessionProvider";

describe("loginSearch (X-96)", () => {
	it("says why only when the tab was signed in before", () => {
		markSignedIn(false);
		expect(loginSearch("/runs")).toEqual({ return_to: "/runs" });
		markSignedIn(true);
		expect(loginSearch("/runs")).toEqual({ return_to: "/runs", reason: "expired" });
		markSignedIn(false);
	});
});
