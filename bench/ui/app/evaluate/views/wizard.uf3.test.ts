import "@/lib/i18n";

import i18n from "i18next";
import { describe, expect, it } from "vitest";
import { plainReason } from "@/components/evaluations/benchmarkChecklist";
import { EMPTY_DRAFT } from "./evaluateDraft";
import { footerSummary, nextReason } from "./evaluateView";
import { goalOf, goalPatch } from "./step2Learn";
import { advancedChanged, defaultConcurrency } from "./step4Setup";
import { addKeyHref, estimateLines } from "./step5Launch";

const t = (key: string, opts?: Record<string, unknown>): string => (i18n.t as unknown as (k: string, o: object) => string)(key, { ns: "evaluate", ...opts });

describe("wizard footer (M3-26)", () => {
	it("says why Next is off on each step", () => {
		expect(nextReason(1, EMPTY_DRAFT)).toBe("needAgent");
		expect(nextReason(2, { ...EMPTY_DRAFT, agent: "codex", mode: "versions" })).toBe("needVersion");
		expect(nextReason(2, { ...EMPTY_DRAFT, agent: "codex", mode: "opponent" })).toBe("needOpponent");
		expect(nextReason(3, { ...EMPTY_DRAFT, agent: "codex" })).toBe("needBenchmark");
		expect(nextReason(5, EMPTY_DRAFT)).toBeNull();
	});

	it("sums up the estimate", () => {
		const est = { runs: 20, est_cost_usd: { low: 0.1, high: 0.4, known: true }, est_minutes: { low: 5, high: 12 } };
		expect(footerSummary(t, 2, est)).toBe("2 benchmarks, 20 runs, about $0.10 to $0.40, about 12 min");
		expect(footerSummary(t, 2, { ...est, est_cost_usd: { low: 0, high: 0, known: false } })).toBe("2 benchmarks, 20 runs, price not known yet");
		expect(footerSummary(t, 2, undefined)).toBe("");
	});
});

describe("step 2 goals (M3-48)", () => {
	it("maps the red-team goal to a score evaluation", () => {
		expect(goalPatch("redteam")).toEqual({ mode: "score", redTeam: true });
		expect(goalOf({ mode: "score", redTeam: true })).toBe("redteam");
		expect(goalOf({ mode: "versions", redTeam: true })).toBe("versions");
	});
});

describe("step 3 refusals (M3-33)", () => {
	it("reads a refusal as a plain need", () => {
		expect(plainReason("agent codex cannot run benchmark webarena-pro: needs browser", t)).toBe("Needs a browser agent");
		expect(plainReason("Agent bot cannot run benchmark x: turn on tool access.", t)).toBe("turn on tool access.");
	});
});

describe("step 4 defaults (M3-29, M3-31)", () => {
	it("defaults Runs at once to the worker slots", () => {
		expect(defaultConcurrency({ worker_slots: 2, concurrency: 4 })).toBe(2);
		expect(defaultConcurrency(undefined)).toBe(2);
	});
	it("opens More settings when a fault is chosen", () => {
		const base = { faults: ["clean"] } as never;
		expect(advancedChanged(base)).toBe(false);
		expect(advancedChanged({ faults: ["clean", "model-down"] } as never)).toBe(true);
	});
});

describe("step 5 (M3-27, M3-28)", () => {
	it("links to Model keys with the provider and a way back", () => {
		expect(addKeyHref("openai")).toBe("/settings?tab=keys&add=openai&return=%2Fevaluate%3Fstep%3D5");
	});
	it("says runs, minutes and what happens after Launch", () => {
		const lines = estimateLines(t, { runs: 10, est_minutes: { low: 3, high: 8 } });
		expect(lines[0]).toBe("10 runs, about 3 to 8 min.");
		expect(lines[1]).toContain("cancel at any time");
	});
});
