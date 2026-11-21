import type { ExperimentStatus, LiveEvent } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import {
	elapsedBetween,
	formatElapsed,
	initialLiveState,
	liveExperimentReducer,
	LIVE_EVENTS_KEEP,
	orderedSlots,
	type LiveExperimentState,
} from "./liveExperiment.reducer";

const status = (state: ExperimentStatus["state"] = "running"): ExperimentStatus => ({
	exp_id: "e1",
	name: "demo",
	state,
	manifest_hash: "sha256:x",
	frozen_at: "2026-11-21T10:00:00Z",
	started_at: "2026-11-21T10:00:00Z",
	slots: { total: 4, done: 0, running: 1, failed: 0 },
	spent_usd: { state: "observed", v: 0.5, evidence: "gateway" } as ExperimentStatus["spent_usd"],
});

const ev = (seq: number, kind: string, extra: Partial<LiveEvent> = {}): LiveEvent => ({
	seq,
	kind,
	at: `2026-11-21T10:00:${String(seq).padStart(2, "0")}Z`,
	...extra,
});

function run(events: LiveEvent[], start: LiveExperimentState = initialLiveState()): LiveExperimentState {
	return liveExperimentReducer(start, { type: "poll", page: { events, next: events.at(-1)?.seq ?? 0, status: status() } });
}

describe("liveExperimentReducer", () => {
	it("tracks a remote slot through run_started and stage events", () => {
		const s = run([
			ev(1, "stage", { slot_id: "s1", stage: "leased", message: "worker w-2" }),
			ev(2, "run_started", { slot_id: "s1", run_id: "r1", message: "attempt 1" }),
			ev(3, "stage", { slot_id: "s1", run_id: "r1", stage: "run" }),
		]);
		const slot = s.slots.s1;
		expect(slot.state).toBe("running");
		expect(slot.stage).toBe("run");
		expect(slot.runId).toBe("r1");
		expect(slot.message).toBe("attempt 1");
		expect(slot.startedAt).toBe("2026-11-21T10:00:01Z");
		expect(s.since).toBe(3);
		expect(s.status?.state).toBe("running");
	});

	it("resolves run-only events to their slot and seals by outcome", () => {
		let s = run([ev(1, "slot_started", { slot_id: "s1", run_id: "r1" }), ev(2, "slot_started", { slot_id: "s2", run_id: "r2" })]);
		s = run([ev(3, "run_sealed", { run_id: "r1", outcome: "pass" }), ev(4, "slot_sealed", { slot_id: "s2", outcome: "infra_error" })], s);
		expect(s.slots.s1).toMatchObject({ state: "done", outcome: "pass", stage: "seal", finishedAt: "2026-11-21T10:00:03Z" });
		expect(s.slots.s2.state).toBe("failed");
	});

	it("ignores events already seen and keeps the ticker bounded", () => {
		const s1 = run([ev(1, "slot_started", { slot_id: "s1" })]);
		const s2 = run([ev(1, "slot_started", { slot_id: "s1" })], s1);
		expect(s2.events).toHaveLength(1);
		const many = Array.from({ length: LIVE_EVENTS_KEEP + 10 }, (_, i) => ev(i + 2, "stage", { slot_id: "s1", stage: "run" }));
		const s3 = run(many, s2);
		expect(s3.events).toHaveLength(LIVE_EVENTS_KEEP);
		expect(s3.events.at(-1)?.seq).toBe(LIVE_EVENTS_KEEP + 11);
	});

	it("reopens a slot on a retry and marks skipped and errored slots", () => {
		let s = run([ev(1, "slot_sealed", { slot_id: "s1", outcome: "aborted_infra" }), ev(2, "run_started", { slot_id: "s1", run_id: "r9" })]);
		expect(s.slots.s1).toMatchObject({ state: "running", finishedAt: undefined, outcome: undefined, runId: "r9" });
		s = run([ev(3, "slot_skipped", { slot_id: "s2" }), ev(4, "error", { slot_id: "s3", message: "boom" })], s);
		expect(s.slots.s2.state).toBe("skipped");
		expect(s.slots.s3).toMatchObject({ state: "failed", message: "boom" });
	});

	it("records errors without dropping state, and a poll clears them", () => {
		let s = run([ev(1, "slot_started", { slot_id: "s1" })]);
		s = liveExperimentReducer(s, { type: "error", error: "offline" });
		expect(s.error).toBe("offline");
		expect(s.slots.s1).toBeDefined();
		s = run([], s);
		expect(s.error).toBeUndefined();
	});

	it("orders running slots first, then the newest finished", () => {
		const s = run([
			ev(1, "slot_started", { slot_id: "a" }),
			ev(2, "slot_started", { slot_id: "b" }),
			ev(3, "slot_started", { slot_id: "c" }),
			ev(4, "slot_sealed", { slot_id: "a", outcome: "pass" }),
		]);
		expect(orderedSlots(s).map((x) => x.slotId)).toEqual(["c", "b", "a"]);
		expect(orderedSlots(s, 0).map((x) => x.slotId)).toEqual(["c", "b"]);
	});
});

describe("elapsed helpers", () => {
	it("formats durations", () => {
		expect(formatElapsed(12_400)).toBe("12s");
		expect(formatElapsed(185_000)).toBe("3m 05s");
		expect(formatElapsed(2 * 3600_000 + 4 * 60_000)).toBe("2h 04m");
		expect(formatElapsed(-5)).toBe("0s");
	});
	it("measures to the end or now", () => {
		const now = Date.parse("2026-11-21T10:01:00Z");
		expect(elapsedBetween("2026-11-21T10:00:00Z", undefined, now)).toBe(60_000);
		expect(elapsedBetween("2026-11-21T10:00:00Z", "2026-11-21T10:00:30Z", now)).toBe(30_000);
		expect(elapsedBetween(undefined, undefined, now)).toBeUndefined();
	});
});
