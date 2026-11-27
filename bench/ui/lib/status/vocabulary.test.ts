import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { describe, expect, it } from "vitest";
import { readinessOf, statusOf, verdictOf } from "./vocabulary";

// Spec 4: one word, tone and icon per raw state.
describe("status vocabulary", () => {
	it("maps raw values from every list to the eight states", () => {
		expect(statusOf("cancelled").state).toBe("stopped");
		expect(statusOf("cancelled").tone).toBe("neutral");
		expect(statusOf("budget_stop").state).toBe("stopped");
		expect(statusOf("awaiting_approval").state).toBe("needsYou");
		expect(statusOf("needsLock").state).toBe("needsYou");
		expect(statusOf("Succeeded").state).toBe("finished");
		expect(statusOf("invalid").tone).toBe("warn");
		expect(statusOf("running").tone).toBe("running");
		expect(statusOf("failed").tone).toBe("bad");
		expect(statusOf("what-is-this").state).toBe("noAnswer");
		expect(statusOf(undefined).state).toBe("noAnswer");
	});
	it("has a word for every state", () => {
		for (const raw of ["queued", "running", "pending_review", "done", "error", "paused", "broken", "unknown"]) {
			const key = statusOf(raw).labelKey.slice("status:".length);
			expect(i18n.exists(key, { ns: "status" }), raw).toBe(true);
		}
		expect(i18n.t("stopped", { ns: "status" })).toBe("Stopped");
	});
	it("has small readiness and verdict families", () => {
		expect(readinessOf(false).tone).toBe("neutral");
		expect(verdictOf("regressed").tone).toBe("bad");
		expect(verdictOf("inconclusive").state).toBe("tooFew");
	});
});
