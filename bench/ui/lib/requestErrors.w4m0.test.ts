import { describe, expect, it } from "vitest";
import "@/lib/i18n";
import { canRetry, transportErrorText } from "@/lib/requestErrors";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";

describe("transportErrorText (wave 4, p07-4 and p10-3)", () => {
	it("says a network failure in plain words", () => {
		expect(transportErrorText({ status: "FETCH_ERROR", error: "TypeError: Failed to fetch" })).toBe(
			"Could not reach the server. Check your connection, then try again.",
		);
	});
	it("says a time-out in plain words", () => {
		expect(transportErrorText({ status: "TIMEOUT_ERROR", error: "AbortError" })).toBe("The server took too long to answer. Try again.");
	});
	it("says a plain-text 413 is a file too large", () => {
		expect(
			transportErrorText({ status: "PARSING_ERROR", originalStatus: 413, data: "Request Entity Too Large", error: "SyntaxError: x" }),
		).toBe("File too large for the server.");
	});
	it("says a plain-text 502 could not be read, with its status", () => {
		const text = transportErrorText({ status: "PARSING_ERROR", originalStatus: 502, data: "Bad Gateway", error: "SyntaxError: x" });
		expect(text).toBe("Unreadable server answer (status 502). Try again.");
	});
	it("is null for a JSON answer", () => {
		expect(transportErrorText({ status: 400, data: { error: { message: "bad name" } } })).toBeNull();
		expect(transportErrorText(undefined)).toBeNull();
	});
});

describe("canRetry (wave 4)", () => {
	it("is false for answers a retry cannot change and true otherwise", () => {
		expect(canRetry({ status: 400 })).toBe(false);
		expect(canRetry({ status: 409 })).toBe(false);
		expect(canRetry({ status: 500 })).toBe(true);
		expect(canRetry({ status: "FETCH_ERROR", error: "TypeError: Failed to fetch" })).toBe(true);
	});
});

describe("getBenchErrorMessage with transport errors (wave 4, p07-4 and p10-3)", () => {
	it("never shows the raw TypeError of a failed fetch", () => {
		const text = getBenchErrorMessage({ status: "FETCH_ERROR", error: "TypeError: Failed to fetch" });
		expect(text).not.toContain("TypeError");
		expect(text).toBe("Could not reach the server. Check your connection, then try again.");
	});
	it("gives the size sentence for a plain-text 413", () => {
		const text = getBenchErrorMessage({
			status: "PARSING_ERROR",
			originalStatus: 413,
			data: "too large",
			error: "SyntaxError: Unexpected token",
		});
		expect(text).toBe("File too large for the server.");
	});
	it("still gives the server's message for a JSON 400", () => {
		expect(getBenchErrorMessage({ status: 400, data: { error: { message: "bad name" } } })).toBe("bad name");
	});
});