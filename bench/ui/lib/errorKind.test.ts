import { describe, expect, it } from "vitest";
import { errorKind, kindRetryable } from "@/lib/errorKind";

describe("errorKind (Spec 5 error pattern)", () => {
	it("maps transport failures", () => {
		expect(errorKind({ status: "FETCH_ERROR", error: "x" })).toBe("network");
		expect(errorKind({ status: "TIMEOUT_ERROR" })).toBe("timeout");
	});
	it("maps statuses", () => {
		expect(errorKind({ status: 401 })).toBe("signedOut");
		expect(errorKind({ status: 403 })).toBe("forbidden");
		expect(errorKind({ status: 404 })).toBe("notFound");
		expect(errorKind({ status: 409 })).toBe("conflict");
		expect(errorKind({ status: 422 })).toBe("invalid");
		expect(errorKind({ status: 429 })).toBe("rateLimited");
		expect(errorKind({ status: 503 })).toBe("server");
		expect(errorKind({ status: "PARSING_ERROR", originalStatus: 502 })).toBe("server");
	});
	it("lets a server error type win over the status", () => {
		expect(errorKind({ status: 402, data: { error: { type: "plan_limit" } } })).toBe("planLimit");
		expect(errorKind({ status: 400, data: { error: { type: "no_model_key" } } })).toBe("noModelKey");
	});
	it("is unknown for anything else", () => {
		expect(errorKind(null)).toBe("unknown");
		expect(errorKind(new Error("boom"))).toBe("unknown");
	});
	it("offers Retry only where it can help", () => {
		expect(kindRetryable("server")).toBe(true);
		expect(kindRetryable("network")).toBe(true);
		expect(kindRetryable("notFound")).toBe(false);
		expect(kindRetryable("forbidden")).toBe(false);
		expect(kindRetryable("invalid")).toBe(false);
	});
});
