import type { ReleaseRecord } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import {
	EMPTY_RELEASE_FORM,
	canRelease,
	chainLinks,
	formatReleaseTime,
	newestFirst,
	quickRunCount,
	releasableExperiments,
	releaseBlockers,
	releaseDisabledReason,
	releaseRequestFrom,
	shortHash,
	verdictPolicyDecision,
} from "./releases.utils";

function record(id: string, createdAt: string, prev: string, hash: string): ReleaseRecord {
	return {
		release_id: id,
		exp_id: "e_x",
		analysis_id: "a_x",
		arm: "B",
		agent_config_id: "agent_b",
		config_hash: "sha256:abc",
		decision: "improved",
		action: "ship",
		manifest_hash: "sha256:m",
		seals_digest: "d",
		approver: "ana",
		created_at: createdAt,
		prev_hash: prev,
		hash,
	};
}

describe("newestFirst", () => {
	it("orders by created_at, later log line first on ties", () => {
		const recs = [
			record("r1", "2026-11-10T00:00:00Z", "", "h1"),
			record("r2", "2026-11-12T00:00:00Z", "h1", "h2"),
			record("r3", "2026-11-12T00:00:00Z", "h2", "h3"),
		];
		expect(newestFirst(recs).map((r) => r.release_id)).toEqual(["r3", "r2", "r1"]);
		expect(recs[0].release_id).toBe("r1");
	});
});

describe("chainLinks", () => {
	it("marks a record whose prev_hash does not name the record before it", () => {
		const recs = [
			record("r1", "2026-11-10T00:00:00Z", "", "h1"),
			record("r2", "2026-11-11T00:00:00Z", "h1", "h2"),
			record("r3", "2026-11-12T00:00:00Z", "hX", "h3"),
		];
		expect(chainLinks(recs)).toEqual({ r1: true, r2: true, r3: false });
		expect(chainLinks([record("r9", "", "h0", "h9")])).toEqual({ r9: false });
	});
});

describe("formatting", () => {
	it("shortens hashes and prints UTC times", () => {
		expect(shortHash("sha256:0123456789abcdef")).toBe("0123456789ab");
		expect(shortHash("abc")).toBe("abc");
		expect(formatReleaseTime("2026-11-14T08:05:59.9Z")).toBe("2026-11-14 08:05 UTC");
		expect(formatReleaseTime("nope")).toBe("nope");
	});
});

describe("release rule", () => {
	const ready = { ...EMPTY_RELEASE_FORM, approver: "ana" };
	const cases = [
		{ name: "ship, no policy", action: "ship", policy: undefined, values: ready, blockers: [], want: true },
		{ name: "ship, allow", action: "ship", policy: "allow", values: ready, blockers: [], want: true },
		{ name: "ship, warn", action: "ship", policy: "warn", values: ready, blockers: [], want: true },
		{ name: "no approver", action: "ship", policy: "allow", values: EMPTY_RELEASE_FORM, blockers: [], want: false },
		{ name: "hold", action: "hold", policy: undefined, values: ready, blockers: ["notShip"], want: false },
		{ name: "ship, block", action: "ship", policy: "block", values: ready, blockers: ["policyBlock"], want: false },
		{ name: "both", action: "no_decision", policy: "block", values: ready, blockers: ["notShip", "policyBlock"], want: false },
		{
			name: "override without reason",
			action: "hold",
			policy: undefined,
			values: { ...ready, override: true },
			blockers: ["notShip"],
			want: false,
		},
		{
			name: "override with reason",
			action: "hold",
			policy: "block",
			values: { ...ready, override: true, overrideReason: "hotfix" },
			blockers: ["notShip", "policyBlock"],
			want: true,
		},
	] as const;
	for (const c of cases) {
		it(c.name, () => {
			expect(releaseBlockers(c.action, c.policy)).toEqual(c.blockers);
			expect(canRelease(true, c.action, c.policy, c.values)).toBe(c.want);
		});
	}
	it("needs a loaded verdict", () => {
		expect(canRelease(false, undefined, undefined, ready)).toBe(false);
	});
});

describe("releaseRequestFrom", () => {
	it("sends the override only when the release is blocked", () => {
		const values = { approver: " ana ", note: " ", override: true, overrideReason: " hotfix " };
		expect(releaseRequestFrom("e_x", "a_x", true, values)).toEqual({
			exp_id: "e_x",
			analysis_id: "a_x",
			approver: "ana",
			override: "hotfix",
		});
		expect(releaseRequestFrom("e_x", undefined, false, values)).toEqual({ exp_id: "e_x", approver: "ana" });
	});
});

describe("verdictPolicyDecision", () => {
	it("reads the optional policy of a verdict response", () => {
		expect(verdictPolicyDecision({ policy: { decision: "block" } })).toBe("block");
		expect(verdictPolicyDecision({ policy: { decision: "maybe" } })).toBeUndefined();
		expect(verdictPolicyDecision({})).toBeUndefined();
		expect(verdictPolicyDecision(undefined)).toBeUndefined();
	});
});
describe("releasableExperiments", () => {
	it("keeps only finished experiments, in order, so a running one is never picked by default", () => {
		const list = [
			{ exp_id: "e_running", name: "running one", state: "running" as const },
			{ exp_id: "e_done", name: "prompt v2", state: "done" as const },
			{ exp_id: "e_analyzing", name: "analyzing one", state: "analyzing" as const },
			{ exp_id: "e_failed", name: "failed one", state: "failed" as const },
			{ exp_id: "e_done2", name: "model switch", state: "done" as const },
		];
		expect(releasableExperiments(list).map((e) => e.exp_id)).toEqual(["e_done", "e_done2"]);
		expect(releasableExperiments([])).toEqual([]);
	});
	it("drops readiness checks, practice runs and quick runs unless quick runs are asked for", () => {
		const done = "done" as const;
		const list = [
			{
				exp_id: "e_admit_acme_voice_desk_1a2b",
				name: "admit-acme-voice-desk",
				state: done,
				suite: "acme-voice-desk@1.0.0",
				arms: ["oracle", "noop"],
			},
			{ exp_id: "e_admit_acme_safety_5e8q", name: "admit-acme-safety", state: done, suite: "acme-safety@1.0.0", arms: ["oracle", "noop"] },
			{ exp_id: "e_practice", name: "try it", state: done, suite: "coding-core@1.0.0", arms: ["A", "B"] },
			{ exp_id: "e_quick", name: "quick-20261121-101500-ab12", state: done, suite: "tb2@1.0.0", arms: ["A", "B"] },
			{ exp_id: "e_real", name: "prompt v2 vs v1", state: done, suite: "tb2@1.0.0", arms: ["A", "B"] },
		];
		expect(releasableExperiments(list).map((e) => e.exp_id)).toEqual(["e_real"]);
		expect(releasableExperiments(list, { quick: true }).map((e) => e.exp_id)).toEqual(["e_quick", "e_real"]);
		expect(quickRunCount(list)).toBe(1);
	});
});
describe("releasableExperiments: agent version comparisons only (N170, N29)", () => {
	it("leaves out model-only and A/A comparisons and debug runs, and keeps lists from older servers", () => {
		const done = "done" as const;
		const list = [
			{ exp_id: "e_model", name: "codex: two models", state: done, arms: ["A", "B"], arms_differ: "model" as const },
			{ exp_id: "e_aa", name: "codex twice", state: done, arms: ["A", "B"], arms_differ: "none" as const },
			{ exp_id: "e_agent", name: "codex v2 vs v1", state: done, arms: ["A", "B"], arms_differ: "agent" as const },
			{ exp_id: "e_old", name: "older server", state: done, arms: ["A", "B"] },
			{ exp_id: "e_dbg", name: "debug copy", state: done, arms: ["A", "B"], arms_differ: "agent" as const, debug: "replay" },
			{ exp_id: "e_replay_r_abc123", name: "replay of a run", state: done, arms: ["A", "B"] },
		];
		expect(releasableExperiments(list).map((e) => e.exp_id)).toEqual(["e_agent", "e_old"]);
	});
});

describe("releaseDisabledReason (N170)", () => {
	const base = {
		expId: "e_x",
		verdictLoading: false,
		verdictError: undefined,
		verdictLoaded: true,
		action: "ship" as const,
		policy: undefined,
	};
	it("says the first thing that keeps the button off", () => {
		expect(releaseDisabledReason({ ...base, expId: undefined, values: EMPTY_RELEASE_FORM })).toEqual({ key: "pick" });
		expect(releaseDisabledReason({ ...base, verdictLoading: true, verdictLoaded: false, values: EMPTY_RELEASE_FORM })).toEqual({
			key: "loading",
		});
		expect(releaseDisabledReason({ ...base, verdictLoaded: false, verdictError: "boom", values: EMPTY_RELEASE_FORM })).toEqual({
			key: "error",
			error: "boom",
		});
		expect(releaseDisabledReason({ ...base, values: EMPTY_RELEASE_FORM })).toEqual({ key: "approver" });
		const named = { ...EMPTY_RELEASE_FORM, approver: "ana" };
		expect(releaseDisabledReason({ ...base, action: "hold", values: named })).toEqual({ key: "blocked" });
		expect(releaseDisabledReason({ ...base, policy: "block", values: { ...named, override: true } })).toEqual({ key: "reason" });
		expect(releaseDisabledReason({ ...base, values: named })).toBeNull();
		expect(releaseDisabledReason({ ...base, action: "hold", values: { ...named, override: true, overrideReason: "hotfix" } })).toBeNull();
	});
});
describe("releaseLinkMissing", () => {
	it("is true only for a linked id the loaded log does not hold", async () => {
		const { releaseLinkMissing } = await import("./releases.utils");
		const log = [{ release_id: "rel_1" }] as unknown as Parameters<typeof releaseLinkMissing>[0];
		expect(releaseLinkMissing(log, "rel_1")).toBe(false);
		expect(releaseLinkMissing(log, "rel_9")).toBe(true);
		expect(releaseLinkMissing(log, null)).toBe(false);
		expect(releaseLinkMissing([], "rel_9")).toBe(false);
	});
});
