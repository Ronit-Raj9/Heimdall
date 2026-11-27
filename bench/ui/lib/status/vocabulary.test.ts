import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { describe, expect, it } from "vitest";
import { CircleCheck } from "lucide-react";
import { rawValuesOf, readinessOf, statusOf, verdictOf, type StatusState } from "./vocabulary";

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

// Every run outcome the server sends (bench/schemas/run.go Outcome), plus canary and trace import
// outcomes. None may fall back to the "no answer" state silently: only "unknown" says so on purpose.
describe("server outcomes (Q-3)", () => {
	const SERVER: Record<string, StatusState> = {
		pass: "finished",
		fail: "failed",
		timeout: "failed",
		budget: "failed",
		crash: "failed",
		refusal: "failed",
		integrity_violation: "failed",
		invalid_evaluator: "broken",
		infra_error: "broken",
		aborted_infra: "broken",
		unknown: "noAnswer",
		pinned: "finished",
		clear: "finished",
		flagged: "needsYou",
		invalid: "broken",
		stale: "needsYou",
		refused: "failed",
		error: "failed",
		ok: "finished",
	};
	for (const [raw, state] of Object.entries(SERVER)) {
		it(`maps ${raw} to ${state}, listed explicitly`, () => {
			expect(statusOf(raw).state).toBe(state);
			expect(rawValuesOf(state)).toContain(raw);
		});
	}
	it("gives a solved run the check icon", () => {
		expect(statusOf("pass").icon).toBe(CircleCheck);
		expect(statusOf("solved").icon).toBe(CircleCheck);
	});
});
