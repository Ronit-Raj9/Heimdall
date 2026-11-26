import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { errorMessage, plainScheduleError } from "./schedulesApi";

describe("schedule error text", () => {
	it("drops the leading schedule name the server puts before its reason", () => {
		expect(plainScheduleError("schedule x: cron: expected 5 fields")).toBe("cron: expected 5 fields");
		expect(plainScheduleError("cron: expected 5 fields")).toBe("cron: expected 5 fields");
	});

	it("says a lost connection in plain words, not the fallback", () => {
		expect(errorMessage({ status: "FETCH_ERROR", error: "TypeError: Failed to fetch" }, "unknown error")).toBe(
			"Could not reach the server. Check your connection, then try again.",
		);
	});
});