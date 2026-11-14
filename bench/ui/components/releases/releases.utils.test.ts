import type { ReleaseRecord } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import {
	EMPTY_RELEASE_FORM,
	canRelease,
	chainLinks,
	formatReleaseTime,
	newestFirst,
	releaseBlockers,
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