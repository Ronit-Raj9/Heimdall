import type { SuiteHealth, TaskHealth, TaskQuarantine } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
	evidenceKinds,
	flagCounts,
	formatPercent,
	hasNoInformation,
	healthByTask,
	healthChips,
	healthState,
	healthUnavailableReason,
	heldOutAndQuarantined,
	isHeldOut,
	isRedacted,
	taskQuarantine,
} from "./suiteHealth.utils";

const report = JSON.parse(
	fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/suites.health.acme-payments.json"), "utf8"),
) as SuiteHealth;
const task = (id: string) => report.tasks.find((t) => t.task === id)!;

const quarantine: TaskQuarantine = { reason: "flaky", at: "2026-11-15T10:30:00Z", source: "manual" };

describe("healthUnavailableReason", () => {
	const cases = [
		{ name: "503 means no run store", error: { status: 503, data: {} }, want: "no_store" },
		{ name: "404 means no report", error: { status: 404, data: {} }, want: "not_found" },
		{ name: "500 is another failure", error: { status: 500, data: {} }, want: "error" },
		{ name: "network failure", error: { status: "FETCH_ERROR", error: "offline" }, want: "error" },
		{ name: "no error object", error: undefined, want: "error" },
	];
	for (const c of cases) {
		it(c.name, () => {
			expect(healthUnavailableReason(c.error)).toBe(c.want);
		});
	}
});

describe("healthState", () => {
	it("is loading until the first answer", () => {
		expect(healthState({ isLoading: true, isError: false })).toEqual({ state: "loading" });
	});

	it("is ready with a task index when the report arrives", () => {
		const state = healthState({ isLoading: false, isError: false, data: report });
		expect(state.state).toBe("ready");
		if (state.state === "ready") expect([...state.byTask.keys()]).toEqual(["p01", "p02", "p03"]);
	});

	it("is unavailable, never thrown, on a 503", () => {
		const error = { status: 503, data: { error: { message: "unavailable" } } };
		expect(healthState({ isLoading: false, isError: true, error })).toEqual({ state: "unavailable", reason: "no_store", error });
	});

	it("drops stale data when a refetch fails", () => {
		expect(healthState({ isLoading: false, isError: true, error: { status: 404 }, data: report }).state).toBe("unavailable");
	});

	it("is unavailable when the query was skipped", () => {
		expect(healthState({ isLoading: false, isError: false })).toEqual({ state: "unavailable", reason: "error" });
	});
});

describe("held out and quarantine", () => {
	it("tells held out from redacted", () => {
		expect(isHeldOut({ split: "held_out" })).toBe(true);
		expect(isHeldOut({ split: "public" })).toBe(false);
		expect(isHeldOut({})).toBe(false);
		expect(isRedacted({ redacted: true })).toBe(true);
		expect(isRedacted({})).toBe(false);
	});

	it("prefers the suite list's quarantine, then the report's", () => {
		expect(taskQuarantine({ quarantine }, task("p02"))).toBe(quarantine);
		expect(taskQuarantine({}, task("p02"))?.source).toBe("auto");
		expect(taskQuarantine({}, task("p01"))).toBeNull();
		expect(taskQuarantine(undefined, undefined)).toBeNull();
	});

	it("uses the server's counts, else counts the tasks", () => {
		const tasks = [{ split: "held_out" as const }, { split: "held_out" as const, quarantine }, {}];
		expect(heldOutAndQuarantined({ tasks })).toEqual({ heldOut: 2, quarantined: 1 });
		expect(heldOutAndQuarantined({ tasks, held_out_count: 5, quarantined_count: 0 })).toEqual({ heldOut: 5, quarantined: 0 });
	});
});

describe("evidenceKinds", () => {
	it("counts per kind in display order, unknown kinds last", () => {
		expect(evidenceKinds(task("p02").verifier)).toEqual([
			{ kind: "regrade_disagrees", count: 1 },
			{ kind: "same_output_differs", count: 2 },
		]);
		const mixed = evidenceKinds({
			evidence: [
				{ kind: "zz_new" as never, detail: "" },
				{ kind: "admission_reference", detail: "" },
				{ kind: "oracle_failed", detail: "" },
			],
		});
		expect(mixed.map((k) => k.kind)).toEqual(["oracle_failed", "admission_reference", "zz_new"]);
		expect(evidenceKinds({ evidence: [] })).toEqual([]);
	});
});

describe("hasNoInformation", () => {
	it("is true for always_pass and always_fail only", () => {
		expect(hasNoInformation({ discrimination: "always_pass" })).toBe(true);
		expect(hasNoInformation({ discrimination: "always_fail" })).toBe(true);
		expect(hasNoInformation({ discrimination: "discriminating" })).toBe(false);
		expect(hasNoInformation({ discrimination: "single_config" })).toBe(false);
		expect(hasNoInformation({ discrimination: "unknown" })).toBe(false);
	});
});

describe("formatPercent", () => {
	it("keeps one decimal below 10% and none above; null stays null", () => {
		expect(formatPercent(0.5, "en")).toBe("50%");
		expect(formatPercent(0.077, "en")).toBe("7.7%");
		expect(formatPercent(0, "en")).toBe("0%");
		expect(formatPercent(1, "en")).toBe("100%");
		expect(formatPercent(null, "en")).toBeNull();
		expect(formatPercent(undefined, "en")).toBeNull();
		expect(formatPercent(Number.NaN, "en")).toBeNull();
	});
});

describe("healthChips", () => {
	it("puts quarantine first, then the flaky verifier, then the rest", () => {
		expect(healthChips(task("p02"), task("p02").quarantine).map((c) => [c.key, c.tone])).toEqual([
			["quarantined", "warn"],
			["flaky_verifier", "bad"],
			["difficulty_mismatch", "warn"],
			["infra_trouble", "warn"],
			["timeouts", "warn"],
		]);
	});

	it("folds saturated into no information", () => {
		expect(healthChips(task("p03"), null).map((c) => c.key)).toEqual(["no_discrimination"]);
	});

	it("never turns agent variance into a chip", () => {
		const p01 = task("p01");
		expect(p01.variance.unstable).toBeGreaterThan(0);
		expect(healthChips(p01, null)).toEqual([]);
	});

	it("flags a flaky verifier even when the flag list misses it", () => {
		const flaky: TaskHealth = { ...task("p01"), verifier: { flaky: true, checks: 2, evidence: [] }, flags: [] };
		expect(healthChips(flaky, null).map((c) => c.key)).toEqual(["flaky_verifier"]);
	});

	it("shows only the quarantine when there is no report for the task", () => {
		expect(healthChips(undefined, quarantine).map((c) => c.key)).toEqual(["quarantined"]);
		expect(healthChips(undefined, null)).toEqual([]);
	});
});

describe("flagCounts", () => {
	it("orders known flags by seriousness, drops zeros and keeps unknown flags last", () => {
		expect(flagCounts({ timeouts: 1, zz_new: 2, flaky_verifier: 3, dead: 0 })).toEqual([
			{ flag: "flaky_verifier", count: 3 },
			{ flag: "timeouts", count: 1 },
			{ flag: "zz_new", count: 2 },
		]);
		expect(flagCounts(null)).toEqual([]);
	});
});

describe("healthByTask", () => {
	it("indexes tasks by id and tolerates a null list", () => {
		expect(healthByTask(report).get("p02")?.verifier.flaky).toBe(true);
		expect(healthByTask({ ...report, tasks: null as never }).size).toBe(0);
	});
});