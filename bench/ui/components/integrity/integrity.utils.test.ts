import { describe, expect, it } from "vitest";
import type { MonitorCertificationSummary, OperatingPoint } from "@/lib/types/bench";
import {
	bandScale,
	certificationsFor,
	formatFpr,
	formatInterval,
	formatRate,
	headlinePoint,
	insideBand,
	isHarnessNoise,
	splitOnPhrase,
} from "./integrity.utils";

describe("isHarnessNoise", () => {
	it.each([
		["improved", true],
		["regressed", true],
		["inconclusive", false],
		["invalid", false],
		[undefined, false],
	] as const)("%s -> %s", (decision, want) => {
		expect(isHarnessNoise(decision)).toBe(want);
	});
});

describe("bandScale", () => {
	it("covers the MDE and every marker with headroom, at least 5 pp", () => {
		expect(bandScale(34.6, [0.1]).domain).toBe(44);
		expect(bandScale(2, [0.01]).domain).toBe(5);
		expect(bandScale(10, [-0.5]).domain).toBe(63);
	});
	it("maps zero to the middle and clamps", () => {
		const s = bandScale(10, []);
		expect(s.pos(0)).toBe(50);
		expect(s.pos(1000)).toBe(100);
		expect(s.pos(-1000)).toBe(0);
	});
});

describe("insideBand", () => {
	it.each([
		[0.1, 34.6, true],
		[0.4, 34.6, false],
		[-0.4, 34.6, false],
		[undefined, 34.6, null],
		[0.1, 0, null],
	] as const)("d_bar %s, MDE %s -> %s", (d, mde, want) => {
		expect(insideBand(d, mde)).toBe(want);
	});
});

describe("formatting", () => {
	it("formats rates, FPR targets and intervals", () => {
		expect(formatRate(0.786, "en")).toBe("78.6%");
		expect(formatFpr(0.001, "en")).toBe("0.1%");
		expect(formatFpr(0.01, "en")).toBe("1%");
		expect(formatInterval([0.44, 0.97], "en")).toBe("[44%, 97%]");
		expect(formatInterval(undefined, "en")).toBe("");
		expect(formatRate(null, "en")).toBe("-");
		expect(formatFpr(null, "en")).toBe("-");
	});
});

describe("headlinePoint", () => {
	const point = (fpr: number): OperatingPoint => ({
		target_fpr: fpr,
		supported: true,
		false_positives: 0,
		honest: 100,
		achieved_fpr: 0,
		caught: 1,
		attacks: 2,
		catch_rate: 0.5,
	});
	it("prefers the 1% point", () => {
		expect(headlinePoint([point(0.001), point(0.01)])?.target_fpr).toBe(0.01);
		expect(headlinePoint([point(0.05)])?.target_fpr).toBe(0.05);
		expect(headlinePoint([])).toBeUndefined();
	});
});

describe("certificationsFor", () => {
	const cert = (id: string, at: string, exps: string[]): MonitorCertificationSummary => ({
		cert_id: id,
		created_at: at,
		monitor_name: "keyword",
		monitor_kind: "builtin",
		mode: "transcript",
		exp_ids: exps,
		honest: 10,
		attacks: 2,
	});
	it("keeps the experiment's certifications, newest first", () => {
		const got = certificationsFor(
			[cert("a", "2026-11-01", ["e_1"]), cert("b", "2026-11-02", ["e_1", "e_2"]), cert("c", "2026-11-03", ["e_2"])],
			"e_1",
		);
		expect(got.map((c) => c.cert_id)).toEqual(["b", "a"]);
	});
});

describe("splitOnPhrase", () => {
	it("marks the first case-insensitive match", () => {
		expect(splitOnPhrase("I think This is a test of me", "this is a test")).toEqual({
			before: "I think ",
			match: "This is a test",
			after: " of me",
		});
	});
	it("leaves an excerpt without the phrase whole", () => {
		expect(splitOnPhrase("nothing here", "honeypot")).toEqual({ before: "nothing here", match: "", after: "" });
	});
});