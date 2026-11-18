import { describe, expect, it } from "vitest";
import { INBOX_LIST_FIXTURE } from "./inbox.fixtures";
import {
	acceptDefaults,
	acceptFormErrors,
	acceptRequestFrom,
	canWriteInbox,
	filterInboxItems,
	inboxCount,
	isActionable,
	parseSnoozeDays,
	redactionEntries,
	rejectFormErrors,
	rejectRequestFrom,
	releaseSummary,
	safeExternalHref,
	snoozeRequestFrom,
} from "./inbox.utils";

const suites = INBOX_LIST_FIXTURE.suites;

describe("counts and filters", () => {
	it("totals every state and reads a missing state as zero", () => {
		expect(inboxCount(INBOX_LIST_FIXTURE.counts, "all")).toBe(6);
		expect(inboxCount(INBOX_LIST_FIXTURE.counts, "accepted")).toBe(2);
		expect(inboxCount({ new: 2 }, "snoozed")).toBe(0);
		expect(inboxCount(null, "all")).toBe(0);
	});

	it("filters by state and source on the client too", () => {
		expect(filterInboxItems(INBOX_LIST_FIXTURE.items, "all").length).toBe(6);
		expect(filterInboxItems(INBOX_LIST_FIXTURE.items, "new").map((i) => i.id)).toEqual(["inc-3f9a1c2b7d10"]);
		expect(filterInboxItems(INBOX_LIST_FIXTURE.items, "accepted", "edge-otlp").map((i) => i.id)).toEqual(["inc-7b20c4d9e811"]);
		expect(filterInboxItems(null, "all")).toEqual([]);
	});

	it("takes actions only on new and snoozed items, and writes only on a live server", () => {
		expect(isActionable({ state: "new" })).toBe(true);
		expect(isActionable({ state: "snoozed" })).toBe(true);
		for (const state of ["accepted", "rejected", "duplicate"] as const) expect(isActionable({ state })).toBe(false);
		expect(canWriteInbox("live")).toBe(true);
		expect(canWriteInbox("recorded")).toBe(false);
		expect(canWriteInbox("synthetic")).toBe(false);
		expect(canWriteInbox(undefined)).toBe(false);
	});
});

describe("accept", () => {
	it("starts on the suggested suite and kind", () => {
		expect(acceptDefaults({ suggested_kind: "mcp", suggested_suite: "support" }, suites)).toEqual({
			suite: "support",
			kind: "mcp",
			taskId: "",
		});
	});

	it("leaves the suite unpicked when the suggestion is not offered, unless only one suite is", () => {
		expect(acceptDefaults({ suggested_kind: "qa", suggested_suite: "gone" }, suites).suite).toBe("");
		expect(acceptDefaults({ suggested_kind: "qa" }, [{ id: "only", template: "qa" }]).suite).toBe("only");
	});

	it("needs a suite", () => {
		expect(acceptFormErrors({ suite: " ", kind: "qa", taskId: "" })).toEqual(["suite"]);
		expect(() => acceptRequestFrom({ suite: "", kind: "qa", taskId: "" })).toThrow();
	});

	it("sends suite and kind, and task_id only when one was typed", () => {
		expect(acceptRequestFrom({ suite: "support", kind: "mcp", taskId: "  " })).toEqual({ suite: "support", kind: "mcp" });
		expect(acceptRequestFrom({ suite: " support ", kind: "tool-use", taskId: " refund-timeout " })).toEqual({
			suite: "support",
			kind: "tool-use",
			task_id: "refund-timeout",
		});
	});
});

describe("reject", () => {
	it("requires a reason", () => {
		expect(rejectFormErrors({ reason: "", duplicateOf: "" })).toEqual(["reason"]);
		expect(rejectFormErrors({ reason: "   ", duplicateOf: "inc-1" })).toEqual(["reason"]);
		expect(rejectFormErrors({ reason: "expected behaviour", duplicateOf: "" })).toEqual([]);
		expect(() => rejectRequestFrom({ reason: " ", duplicateOf: "" })).toThrow();
	});

	it("refuses an item as its own duplicate", () => {
		expect(rejectFormErrors({ reason: "same", duplicateOf: "inc-a" }, "inc-a")).toEqual(["duplicateSelf"]);
	});

	it("sends duplicate_of only when one was given", () => {
		expect(rejectRequestFrom({ reason: " expected behaviour ", duplicateOf: "" })).toEqual({ reason: "expected behaviour" });
		expect(rejectRequestFrom({ reason: "same timeout", duplicateOf: " inc-3f9a1c2b7d10 " })).toEqual({
			reason: "same timeout",
			duplicate_of: "inc-3f9a1c2b7d10",
		});
	});
});

describe("snooze", () => {
	it("takes whole days from 1 to 90", () => {
		expect(parseSnoozeDays("7")).toBe(7);
		expect(parseSnoozeDays(" 90 ")).toBe(90);
		for (const bad of ["0", "91", "1.5", "-3", "", "seven"]) expect(parseSnoozeDays(bad)).toBeNull();
		expect(snoozeRequestFrom("14")).toEqual({ days: 14 });
		expect(() => snoozeRequestFrom("0")).toThrow();
	});
});

describe("release summary", () => {
	it("says passes, fails or not run yet", () => {
		expect(releaseSummary({ status: "passes", experiment: "e_123", passed: 3, runs: 3 })).toEqual({
			key: "inbox.release.passes",
			values: { experiment: "e_123", passed: 3, runs: 3 },
		});
		expect(releaseSummary({ status: "fails", passed: 1, runs: 3 }).key).toBe("inbox.release.failsNoExp");
		expect(releaseSummary({ status: "not_run", passed: 0, runs: 0 }).key).toBe("inbox.release.notRun");
		expect(releaseSummary(undefined).key).toBe("inbox.release.notRun");
	});
});

describe("links and redaction", () => {
	it("renders only http and https source links", () => {
		expect(safeExternalHref("https://cloud.langfuse.com/project/p1/traces/abc123")).toBe(
			"https://cloud.langfuse.com/project/p1/traces/abc123",
		);
		expect(safeExternalHref("http://127.0.0.1:3000/trace/1")).toBe("http://127.0.0.1:3000/trace/1");
		for (const bad of ["javascript:alert(1)", "data:text/html,<script>alert(1)</script>", "/relative", "", undefined]) {
			expect(safeExternalHref(bad)).toBeNull();
		}
	});

	it("lists redaction counts largest first and drops zeros", () => {
		expect(redactionEntries({ email: 1, phone: 2, card: 0 })).toEqual([
			["phone", 2],
			["email", 1],
		]);
		expect(redactionEntries(undefined)).toEqual([]);
	});
});