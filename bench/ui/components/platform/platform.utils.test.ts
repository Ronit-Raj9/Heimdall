import { FAILED_JOB, REGRADE_JOB, RUNNING_JOB } from "@/lib/testing/platformFixtures";
import { describe, expect, it } from "vitest";
import {
	durationSeconds,
	formatDuration,
	formatRelative,
	initials,
	isValidPool,
	jobExperimentId,
	jobFraction,
	jobProgress,
	looksLikeEmail,
	runnerStartCommand,
	splitJobs,
	toDate,
	tokenState,
} from "./platform.utils";

describe("times", () => {
	it("reads RFC 3339 and unix milliseconds, and treats Go's zero time as never", () => {
		expect(toDate("2026-11-18T05:31:00Z")?.toISOString()).toBe("2026-11-18T05:31:00.000Z");
		expect(toDate(1_763_443_860_000)?.getTime()).toBe(1_763_443_860_000);
		expect(toDate("0001-01-01T00:00:00Z")).toBeNull();
		expect(toDate(undefined)).toBeNull();
		expect(toDate("not a time")).toBeNull();
	});
	it("says how long ago in the largest unit", () => {
		const now = Date.parse("2026-11-18T06:00:00Z");
		expect(formatRelative("2026-11-18T05:57:00Z", "en", now)).toBe("3 minutes ago");
		expect(formatRelative("2026-11-18T05:59:50Z", "en", now)).toBe("now");
		expect(formatRelative("2026-11-21T06:00:00Z", "en", now)).toBe("in 3 days");
		expect(formatRelative(undefined, "en", now)).toBeNull();
	});
	it("formats durations", () => {
		expect(formatDuration(45)).toBe("45s");
		expect(formatDuration(75)).toBe("1m 15s");
		expect(formatDuration(3700)).toBe("1h 1m");
		expect(formatDuration(null)).toBeNull();
		expect(durationSeconds("2026-11-18T05:00:00Z", "2026-11-18T05:01:30Z")).toBe(90);
		expect(durationSeconds(undefined, undefined)).toBeNull();
	});
});

describe("jobs", () => {
	it("puts running and queued jobs first, oldest first, then the rest newest first", () => {
		const queued = { ...RUNNING_JOB, id: "q", status: "queued" as const, created_at: "2026-11-18T05:35:00Z" };
		const { active, recent } = splitJobs([FAILED_JOB, queued, REGRADE_JOB, RUNNING_JOB]);
		expect(active.map((j) => j.id)).toEqual(["job_run", "q"]);
		expect(recent.map((j) => j.id)).toEqual(["job_fail", "job_rg"]);
	});
	it("reads experiment.run progress as sealed and skipped of all slots", () => {
		expect(jobProgress(RUNNING_JOB)).toEqual({ done: 30, total: 48, label: "running" });
		expect(jobFraction(RUNNING_JOB)).toBeCloseTo(30 / 48);
		expect(jobProgress({ progress: { done: 2, total: 8, message: "locking" } })).toEqual({ done: 2, total: 8, label: "locking" });
		expect(jobFraction({ ...FAILED_JOB, progress: undefined })).toBeNull();
		expect(jobFraction(REGRADE_JOB)).toBe(1);
	});
	it("finds the experiment a job acts on", () => {
		expect(jobExperimentId(RUNNING_JOB)).toBe("e_nightly");
		expect(jobExperimentId(FAILED_JOB)).toBeNull();
		expect(jobExperimentId({ kind: "experiment.run", target: "e_t", params: null })).toBe("e_t");
	});
});

describe("tokens and forms", () => {
	it("tells active, revoked and expired tokens apart", () => {
		const now = Date.parse("2026-11-18T06:00:00Z");
		expect(tokenState({ expires_at: "2027-01-01T00:00:00Z" }, now)).toBe("active");
		expect(tokenState({ expires_at: "2026-11-01T00:00:00Z" }, now)).toBe("expired");
		expect(tokenState({ revoked_at: "2026-11-02T00:00:00Z", expires_at: "2026-11-01T00:00:00Z" }, now)).toBe("revoked");
	});
	it("checks pools and emails loosely and makes initials", () => {
		expect(isValidPool("gpu-a.1")).toBe(true);
		expect(isValidPool("-bad")).toBe(false);
		expect(isValidPool("has space")).toBe(false);
		expect(looksLikeEmail("ada@acme.dev")).toBe(true);
		expect(looksLikeEmail("ada@")).toBe(false);
		expect(initials("Ada Lovelace", "ada@acme.dev")).toBe("AL");
		expect(initials("", "ops@acme.dev")).toBe("O");
	});
	it("uses the server's runner command, or builds one for this origin", () => {
		expect(runnerStartCommand("https://h.dev", "gpu", "bifrost-bench runner start -server x")).toBe("bifrost-bench runner start -server x");
		expect(runnerStartCommand("https://h.dev", "gpu")).toContain("-server https://h.dev");
	});
});