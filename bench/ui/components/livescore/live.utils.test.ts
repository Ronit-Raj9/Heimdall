import { livePullRequest, liveSessionsPath } from "@/lib/store/apis/livescoreApi";
import { describe, expect, it } from "vitest";
import { formatE, formatSignal, inboxLink, resultVariant, seriesRange, sortSignalResults, sparkPoints } from "./live.utils";
import type { LiveSignalDef, LiveSignalResult } from "./livescore.types";

describe("live utils", () => {
	it("formats rates, units and unknown values", () => {
		expect(formatSignal(0.25, "rate")).toBe("25.0%");
		expect(formatSignal(0.00123, "ordinal", "USD")).toBe("$0.0012");
		expect(formatSignal(812.3, "ordinal", "ms")).toBe("812.3 ms");
		expect(formatSignal(2500, "ordinal", "ms")).toBe("2.50 s");
		expect(formatSignal(12345, "ordinal", "tokens")).toBe("12,345 tokens");
		expect(formatSignal(undefined, "rate")).toBe("-");
		expect(formatE(3.6e8)).toBe("3.6e+8");
		expect(formatE(1)).toBe("1.00");
	});

	it("sorts decided signals first and maps results to badges", () => {
		const r = (name: string, decision: LiveSignalResult["decision"], e: number) =>
			({
				name,
				decision,
				e_worse: e,
				e_better: 0,
			}) as unknown as LiveSignalResult;
		expect(sortSignalResults([r("a", "not_yet_known", 9), r("b", "better", 1), r("c", "worse", 2)]).map((x) => x.name)).toEqual([
			"c",
			"b",
			"a",
		]);
		expect(resultVariant("worse")).toBe("destructive");
		expect(resultVariant("better")).toBe("success");
		expect(resultVariant("something new")).toBe("secondary");
	});

	it("draws sparklines on a shared scale with gaps for unknown buckets", () => {
		expect(sparkPoints([0, 1], 0, 1, 100, 10)).toBe("0.0,10.0 100.0,0.0");
		expect(sparkPoints([0.5, null, 0.5], 0, 1, 100, 10)).toBe("0.0,5.0 100.0,5.0");
		const rate = { kind: "rate" } as LiveSignalDef;
		const ord = { kind: "ordinal" } as LiveSignalDef;
		expect(seriesRange([[0.2]], rate)).toEqual([0, 1]);
		expect(seriesRange([[5, null], [9]], ord)).toEqual([0, 9]);
	});

	it("builds the API paths and the inbox link", () => {
		expect(liveSessionsPath({})).toBe("/livescore/sessions");
		expect(liveSessionsPath({ version: "v2", flag: "abandoned", limit: 5 })).toBe("/livescore/sessions?version=v2&flag=abandoned&limit=5");
		expect(livePullRequest()).toEqual({
			url: "/livescore/pull",
			method: "POST",
		});
		expect(inboxLink("inc-0123456789ab")).toBe("/inbox?item=inc-0123456789ab");
	});
});
