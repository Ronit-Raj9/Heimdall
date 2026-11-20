import type { ExperimentStatus, GridRow, Verdict } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import {
	armOrder,
	boundsDiffer,
	cellTone,
	formatPValue,
	formatSignedEffect,
	formatSignedPp,
	integrityTone,
	integrityView,
	meterGroup,
	naiveLeader,
	shortHash,
	sortExperimentsNewestFirst,
	sortGridRows,
	tasksNeededView,
} from "./verdict.utils";
import { verdictFixture, verdictWith } from "./testFixtures";

describe("formatSignedPp", () => {
	const cases = [
		{ v: 0.4167, want: "+41.7" },
		{ v: -0.0912, want: "-9.1" },
		{ v: 0, want: "0" },
		{ v: 0.3333, want: "+33.3" },
	];
	for (const c of cases) it(String(c.v), () => expect(formatSignedPp(c.v, "en")).toBe(c.want));
});

describe("formatSignedEffect", () => {
	// Regression (audit 1-C-07): a seconds endpoint must not print -900 pp.
	const cases = [
		{ m: "verified_success", v: 0.25, want: "+25 pp" },
		{ m: "wall_time_s", v: -9, want: "-9 s" },
		{ m: "latency_ms", v: 12.5, want: "+12.5 ms" },
		{ m: "cost_usd", v: 0.03, want: "+0.03 USD" },
		{ m: undefined, v: 0.1, want: "+10 pp" },
	];
	for (const c of cases) it(`${c.m}:${c.v}`, () => expect(formatSignedEffect(c.m, c.v, "en")).toBe(c.want));
});

describe("formatPValue", () => {
	const cases = [
		{ p: 0.125, want: "0.125" },
		{ p: 0.0078125, want: "0.0078" },
		{ p: 1, want: "1" },
	];
	for (const c of cases) it(String(c.p), () => expect(formatPValue(c.p, "en")).toBe(c.want));
});

describe("shortHash", () => {
	it("drops the algorithm and elides the middle", () => {
		expect(shortHash("sha256:52b5454d8f01de367a0a3baf9d55499ffab1b0b0343bd4585e4cc1c962fc9338")).toBe("52b5454d…9338");
	});
	it("keeps short hashes", () => expect(shortHash("abc123")).toBe("abc123"));
});

describe("tasksNeededView", () => {
	const cases: { name: string; verdict: Verdict; want: ReturnType<typeof tasksNeededView> }[] = [
		{ name: "exact", verdict: verdictWith({ tasks_needed: 7 }), want: { kind: "exact", count: 7 } },
		{ name: "over wins", verdict: verdictWith({ tasks_needed: 7, tasks_needed_over: 200 }), want: { kind: "over", count: 200 } },
		{ name: "none", verdict: verdictWith({ tasks_needed: undefined }), want: null },
	];
	for (const c of cases) it(c.name, () => expect(tasksNeededView(c.verdict)).toEqual(c.want));
});

describe("integrity", () => {
	it("maps unknown strings to none", () => {
		expect(integrityView("")).toBe("none");
		expect(integrityView("unknown")).toBe("unknown");
		expect(integrityView("clean")).toBe("clean");
	});
	const cases = [
		{ integrity: "clean", decision: "improved", want: "good" },
		{ integrity: "clean", decision: "regressed", want: "neutral" },
		{ integrity: "regressed", decision: "improved", want: "bad" },
		{ integrity: "alert", decision: "inconclusive", want: "warn" },
		{ integrity: "none", decision: "invalid", want: "neutral" },
	] as const;
	for (const c of cases) {
		it(`${c.integrity} beside ${c.decision}`, () => expect(integrityTone(c.integrity, c.decision)).toBe(c.want));
	}
});

describe("naiveLeader", () => {
	it("finds the leader and the margin", () => {
		expect(naiveLeader(verdictFixture.naive, ["A", "B"])).toEqual({ leader: "B", by: 6 });
	});
	it("ties", () => {
		expect(naiveLeader({ A: { num: 3, den: 6, unknown: 0 }, B: { num: 3, den: 6, unknown: 0 } }, ["A", "B"])).toEqual({
			leader: null,
			by: 0,
		});
	});
});

describe("armOrder", () => {
	it("reads arms from the primary effect", () => expect(armOrder(verdictFixture)).toEqual(["A", "B"]));
	it("falls back to the naive keys", () => {
		expect(
			armOrder({ verdict: verdictWith({ effect: undefined }), naive: { b2: verdictFixture.naive.B, a1: verdictFixture.naive.A } }),
		).toEqual(["a1", "b2"]);
	});
});

describe("experiments", () => {
	const exp = (id: string, frozen: string) => ({ exp_id: id, frozen_at: frozen }) as ExperimentStatus;
	const list = [exp("e_old", "2026-11-01T00:00:00Z"), exp("e_new", "2026-11-14T00:00:00Z"), exp("e_bad", "not a date")];
	it("sorts newest first", () => expect(sortExperimentsNewestFirst(list).map((e) => e.exp_id)).toEqual(["e_new", "e_old", "e_bad"]));
	it("empty", () => expect(sortExperimentsNewestFirst([])).toEqual([]));
});

describe("cellTone", () => {
	const observed = (v: number) => ({ state: "observed" as const, v });
	const cases = [
		{ outcome: "pass", y: observed(1), want: "pass" },
		{ outcome: "fail", y: observed(0), want: "zero" },
		{ outcome: "timeout", y: observed(0), want: "zero" },
		{ outcome: "integrity_violation", y: observed(0), want: "integrity" },
		{ outcome: "infra_error", y: { state: "unknown" as const, v: null }, want: "unknown" },
		{ outcome: null, y: { state: "unknown" as const, v: null }, want: "unknown" },
		{ outcome: "pass", y: { state: "unknown" as const, v: null }, want: "unknown" },
	] as const;
	for (const c of cases) it(`${c.outcome} ${c.y.state}`, () => expect(cellTone(c.outcome, c.y)).toBe(c.want));
});

describe("sortGridRows", () => {
	it("puts discordant rows first and keeps order otherwise", () => {
		const row = (task: string, discordant: boolean) => ({ task, condition: "clean", discordant }) as GridRow;
		const sorted = sortGridRows([row("t1", false), row("t2", true), row("t3", false), row("t4", true)]);
		expect(sorted.map((r) => r.task)).toEqual(["t2", "t4", "t1", "t3"]);
	});
});

describe("meterGroup", () => {
	const cases = [
		{ name: "false_completion", want: "honesty" },
		{ name: "gates_fired", want: "safety" },
		{ name: "egress_attempts", want: "safety" },
		{ name: "cost_per_verified_success", want: "efficiency" },
		{ name: "new_meter", want: "other" },
	];
	for (const c of cases) it(c.name, () => expect(meterGroup(c.name)).toBe(c.want));
});

describe("boundsDiffer", () => {
	it("false when bounds equal the estimate", () => expect(boundsDiffer(verdictFixture.verdict.effect!)).toBe(false));
	it("true when they differ", () =>
		expect(boundsDiffer({ d_bar: 0.1, lower_bound: { d_bar: -0.1 }, upper_bound: { d_bar: 0.1 } })).toBe(true));
	it("false without bounds", () => expect(boundsDiffer({ d_bar: 0.1 })).toBe(false));
});