import { describe, expect, it } from "vitest";
import "@/lib/i18n";
import { armKeys, firstMissingKey } from "./armKeys";
import { keptKey, keptLink, linkHasOneShot } from "./evaluateView";
import { sameSetup } from "./step2Learn";
import { estimateBasis, estimateBusy, estimateSkip, plainNote, plainNotes, powerText, readyTasks } from "./step4Setup";
import { armText, estimateLines, seedBudget, summaryLines } from "./step5Launch";
import type { EvaluationSpec } from "@/lib/store/apis/evaluationsApi";

const spec: EvaluationSpec = {
	name: "",
	mode: "score",
	arms: [{ label: "A", agent: "codex", model: "gpt-6-luna", provider: "openai" }],
	benchmarks: ["cl-bench"],
	tasks_per_benchmark: 10,
	trials: 1,
	faults: ["clean"],
};

describe("Q-7 step 4 estimate skip", () => {
	it("skips while the held-back spec is not ready even when the live one is", () => {
		expect(estimateSkip(false, false)).toBe(true);
		expect(estimateSkip(false, true)).toBe(false);
		expect(estimateSkip(true, true)).toBe(true);
	});
	it("Next waits while a change is held back or in flight", () => {
		expect(estimateBusy({ pending: true, main: {} }, false)).toBe(true);
		expect(estimateBusy({ pending: false, main: { isFetching: true } }, false)).toBe(true);
		expect(estimateBusy({ pending: false, main: { isFetching: false } }, false)).toBe(false);
		expect(estimateBusy({ pending: true, main: {} }, true)).toBe(false);
	});
});

describe("Q-8 task count agrees with ready tasks", () => {
	const est = { power: { per_benchmark: [{ benchmark: "cl-bench", tasks: 2, decidable: false, mde_pp: null }], overall: { tasks: 2, mde_pp: null }, sd_source: "assumed", advice: "" } };
	it("counts ready tasks when fewer than asked", () => {
		expect(readyTasks(spec, est)).toBe(2);
		expect(readyTasks({ tasks_per_benchmark: 2 }, est)).toBeNull();
		expect(readyTasks(spec, undefined)).toBeNull();
	});
	it("summary says up to N tasks (M ready)", () => {
		const lines = summaryLines(spec, undefined, est);
		expect(lines.some((l) => l.includes("Use up to 10 tasks from each benchmark (2 ready in all)"))).toBe(true);
		expect(lines).toContain("Run 1 benchmark: cl-bench.");
	});
});

describe("Q-9 power line", () => {
	it("never prints a change above 100 points", () => {
		const p = powerText({ power: { overall: { tasks: 2, mde_pp: 299 }, per_benchmark: [{ benchmark: "x", tasks: 2, decidable: true, mde_pp: 299 }], sd_source: "assumed", advice: "" } });
		expect(p?.line).toBe("Too few tasks to see any change.");
		const q = powerText({ power: { overall: { tasks: 40, mde_pp: 20 }, per_benchmark: [{ benchmark: "x", tasks: 40, decidable: true, mde_pp: 150 }], sd_source: "assumed", advice: "" } });
		expect(q?.line).toBe("These settings can detect a change of about 20 points overall (too few tasks to decide per benchmark).");
	});
});

describe("Q-10 basis", () => {
	it("rough list price is not 'from past runs'", () => {
		expect(estimateBasis({ per_benchmark: [{ benchmark: "x", estimate_basis: "list_price" }], est_cost_usd: { known: true } })).toBe("rough");
		expect(estimateBasis({ per_benchmark: [], est_cost_usd: { known: true } })).toBe("own");
		expect(estimateBasis({ per_benchmark: [], from_library: true })).toBe("library");
		expect(estimateBasis({ per_benchmark: [], est_cost_usd: { known: false } })).toBeNull();
	});
});

describe("Q-11 keys and minutes", () => {
	const catalog = [{ id: "openai", name: "OpenAI", models: [{ id: "gpt-6-luna", purposes: [], rank: 0 }] }] as never;
	it("finds the missing key per setup", () => {
		const rows = armKeys(spec.arms, catalog, []);
		expect(rows).toEqual([{ label: "A", provider: "openai", providerName: "OpenAI", state: "none" }]);
		expect(firstMissingKey(rows)?.provider).toBe("openai");
		expect(firstMissingKey(armKeys(spec.arms, catalog, [{ provider: "openai", status: "ok", models: [] }] as never))).toBeUndefined();
		expect(armKeys([{ label: "A", model: "" }], catalog, [])).toEqual([]);
	});
	it("prints 1 min, not 1 to 1 min", () => {
		const t = (k: string, o?: Record<string, unknown>) => `${k}:${o?.min}:${o?.max ?? ""}`;
		expect(estimateLines(t, { runs: 2, est_minutes: { low: 0.6, high: 1.2 } })[0]).toBe("launch.runsTimeOne:1:");
	});
});

describe("Q-12 budget seed", () => {
	const advice = { suggested_usd: 31 };
	it("seeds only from a settled, known estimate, once", () => {
		expect(seedBudget({ seeded: false, fetching: false, advice, costKnown: true })).toBe(31);
		expect(seedBudget({ seeded: false, fetching: true, advice, costKnown: true })).toBeUndefined();
		expect(seedBudget({ seeded: false, fetching: false, advice, costKnown: false })).toBeUndefined();
		expect(seedBudget({ seeded: true, fetching: false, advice, costKnown: true })).toBeUndefined();
		expect(seedBudget({ seeded: false, fetching: false, advice: undefined, costKnown: true })).toBeUndefined();
	});
	it("summary budget line uses the Launch label rounding", () => {
		expect(summaryLines({ ...spec, budget_usd: 31 }).at(-1)).toBe("Stop when spending reaches $31.");
	});
});

describe("Q-13 and Q-266 kept link params", () => {
	const none = { agent: null, mode: null, suite: null };
	it("follows the draft and keeps a linked suite step 3 dropped", () => {
		expect(keptLink({ agent: "codex", mode: "score", benchmarks: ["a"] }, none)).toEqual({ agent: "codex", mode: null, suite: "a" });
		expect(keptLink({ agent: "codex", mode: "models", benchmarks: [] }, { ...none, suite: "zz-test-csv" })).toEqual({
			agent: "codex",
			mode: "models",
			suite: "zz-test-csv",
		});
		expect(keptLink({ agent: "codex", mode: "score", benchmarks: ["a", "b"] }, { ...none, suite: "c" }).suite).toBeNull();
	});
	it("one-shot params still apply once", () => {
		expect(linkHasOneShot({ ...none, opponent: null, keep: "1" })).toBe(true);
		expect(linkHasOneShot({ ...none, agent: "x", opponent: null, keep: null })).toBe(false);
		expect(keptKey({ agent: "x", mode: null, suite: null })).toBe(keptKey({ agent: "x", mode: undefined as never, suite: null }));
	});
});

describe("Q-66 server notes", () => {
	it("maps the same-config note, strips arm prefixes, formats tokens, dedupes", () => {
		expect(plainNote("arms A and B have the same config hash abc: an experiment whose arms do not differ cannot start")).toBe(
			"Pick a second model to see the price.",
		);
		expect(plainNote("arm A: rough: about 23991 tokens per run")).toBe("About 23,991 tokens per run");
		expect(plainNotes(["arm A: x", "arm B: x", ""])).toEqual(["X"]);
	});
});

describe("Q-72 and Q-256", () => {
	it("two identical setups read as one", () => {
		expect(sameSetup({ agent: "codex", model: "m" }, { agent: "codex", model: "m" })).toBe(true);
		expect(sameSetup({ agent: "codex", model: "m" }, { agent: "codex", model: "n" })).toBe(false);
	});
	it("step 5 names the model without its provider", () => {
		expect(armText(spec.arms[0])).toBe("codex latest version on gpt-6-luna");
	});
});

describe("Q-304 and Q-305", () => {
	it("lists the 3 closest to ready, plus the linked one", async () => {
		const { notReadyShown } = await import("./step3Benchmarks");
		const list = [{ id: "a", task_count: 0 }, { id: "b", task_count: 5 }, { id: "c", task_count: 2 }, { id: "d", task_count: 9 }, { id: "e" }];
		expect(notReadyShown(list, null, false).map((s) => s.id)).toEqual(["d", "b", "c"]);
		expect(notReadyShown(list, "e", false).map((s) => s.id)).toEqual(["d", "b", "c", "e"]);
		expect(notReadyShown(list, null, true)).toHaveLength(5);
	});
	it("puts a draft aside only when a real, different one exists", async () => {
		const { realEarlierDraft } = await import("./evaluateView");
		const next = { agent: "codex", mode: "score" as const, benchmarks: ["cl-bench"], pack: null };
		expect(realEarlierDraft({ agent: null, mode: "score", benchmarks: [], pack: null }, next)).toBe(false);
		expect(realEarlierDraft({ ...next }, next)).toBe(false);
		expect(realEarlierDraft({ agent: "codex", mode: "score", benchmarks: [], pack: null }, next)).toBe(false);
		expect(realEarlierDraft({ agent: "other", mode: "score", benchmarks: ["x"], pack: null }, next)).toBe(true);
		expect(realEarlierDraft({ agent: "codex", mode: "score", benchmarks: ["x"], pack: null }, next)).toBe(true);
	});
});
