import { describe, expect, it } from "vitest";
import {
	BARE_MODEL_AGENT,
	EMPTY_DRAFT,
	agentPickPatch,
	applyPatch,
	defaultName,
	draftFromLink,
	draftToSpec,
	learnStepReady,
	specToPatch,
	withBareModel,
} from "./evaluateDraft";

describe("draftToSpec", () => {
	it("starts from the Quick look size and carries concurrency and preset", () => {
		const spec = draftToSpec({
			...EMPTY_DRAFT,
			agent: "codex",
			arms: [{ label: "A", agent: "codex", model: "m" }],
			benchmarks: ["x"],
			concurrency: 8,
		});
		expect(spec.tasks_per_benchmark).toBe(10);
		expect(spec.trials).toBe(1);
		expect(spec.preset).toBe("quick");
		expect(spec.concurrency).toBe(8);
		expect(specToPatch({ ...spec, preset: undefined, concurrency: undefined })).toMatchObject({ preset: null, concurrency: null });
	});

	it("runs the bare-model baseline on arm A's model", () => {
		const arms = withBareModel([
			{ label: "A", agent: "codex", model: "gpt-5", provider: "openai" },
			{ label: "B", agent: BARE_MODEL_AGENT, model: "old", provider: "deepseek" },
		]);
		expect(arms[1]).toEqual({ label: "B", agent: BARE_MODEL_AGENT, model: "gpt-5", provider: "openai" });
		const plain = [
			{ label: "A", agent: "codex", model: "gpt-5" },
			{ label: "B", agent: "claude-code", model: "x" },
		];
		expect(withBareModel(plain)).toEqual(plain);
	});

	it("keeps model B its own when both arms are the model only agent (PQ2-9)", () => {
		const two = [
			{ label: "A", agent: BARE_MODEL_AGENT, model: "gpt-5", provider: "openai" },
			{ label: "B", agent: BARE_MODEL_AGENT, model: "claude-x", provider: "anthropic" },
		];
		expect(withBareModel(two)).toEqual(two);
	});
});

describe("draft round trip (F26)", () => {
	const base = applyPatch(EMPTY_DRAFT, { agent: "codex", arms: [{ label: "A", agent: "codex", model: "m" }], benchmarks: ["x"] });
	const gate = { max_regression: 0.05, fail_on_inconclusive: true };

	it("keeps the release gate, stream and disclose_budget through a step's edit", () => {
		const edited = { ...draftToSpec(base), gate, stream: { memory: "carry" as const }, disclose_budget: true };
		const d = applyPatch(base, specToPatch(edited));
		const spec = draftToSpec(d);
		expect(spec.gate).toEqual(gate);
		expect(spec.stream).toEqual({ memory: "carry" });
		expect(spec.disclose_budget).toBe(true);
		const off = draftToSpec(applyPatch(d, specToPatch({ ...spec, gate: undefined, stream: undefined, disclose_budget: undefined })));
		expect("gate" in off).toBe(false);
		expect("stream" in off).toBe(false);
		expect("disclose_budget" in off).toBe(false);
	});

	it("carries a typed name and falls back to the default name for a blank one", () => {
		expect(draftToSpec({ ...base, name: "  zz-test-compare " }).name).toBe("zz-test-compare");
		expect(draftToSpec({ ...base, name: "   " }).name).toBe(defaultName(base));
		const typed = applyPatch(base, specToPatch({ ...draftToSpec(base), name: "my eval" }, base));
		expect(typed.name).toBe("my eval");
		expect(draftToSpec(typed).name).toBe("my eval");
		// The default name is not frozen into the draft: it still follows the mode.
		const dflt = applyPatch(base, specToPatch(draftToSpec(base), base));
		expect(dflt.name).toBe("");
		expect(draftToSpec(applyPatch(dflt, { mode: "models" })).name).toBe("codex: two models");
	});
});
describe("defaultName (N161)", () => {
	const d = { ...EMPTY_DRAFT, agent: "codex", mode: "score" as const };
	it("names a score evaluation after its benchmarks", () => {
		expect(defaultName({ ...d, benchmarks: ["hd-harbor-bfcl"] })).toBe("codex on hd-harbor-bfcl");
		expect(defaultName({ ...d, benchmarks: ["hd-harbor-bfcl@1.0.0", "b2", "b3"] })).toBe("codex on hd-harbor-bfcl and 2 more");
		expect(defaultName({ ...d, benchmarks: [] })).toBe("codex score");
	});

	it("adds the model, and names a pack run after its pack (M3-52)", () => {
		const arms = [{ label: "A", agent: "codex", model: "gpt-5.6-terra" }];
		expect(defaultName({ ...d, version: 1, arms, benchmarks: ["hd-harbor-tb2", "b2", "b3"] })).toBe(
			"codex v1 on gpt-5.6-terra: hd-harbor-tb2 and 2 more",
		);
		expect(defaultName({ ...d, arms, benchmarks: [], pack: "coding" })).toBe("codex on gpt-5.6-terra: coding pack");
	});

	it("names a score evaluation with faults a failure test", () => {
		expect(defaultName({ ...d, version: 3, benchmarks: ["hd-harbor-bfcl", "b2"], faults: ["clean", "model-down"] })).toBe(
			"codex v3 failure test on hd-harbor-bfcl and 1 more",
		);
		expect(defaultName({ ...d, benchmarks: ["hd-harbor-bfcl"], faults: ["clean"] })).toBe("codex on hd-harbor-bfcl");
	});
});

describe("draftFromLink (N154, N112)", () => {
	it("starts versions mode with no old benchmarks and the agent's own model", () => {
		const { draft, badMode } = draftFromLink({ agent: "zz-test-loop", mode: "versions" }, { model: "gpt-5.4-mini" });
		expect(badMode).toBeUndefined();
		expect(draft.mode).toBe("versions");
		expect(draft.agent).toBe("zz-test-loop");
		expect(draft.benchmarks).toEqual([]);
		expect(draft.pack).toBeNull();
		expect(draft.version).toBeNull();
		expect(draft.compareVersion).toBeNull();
		expect(draft.arms.map((a) => [a.label, a.agent, a.model])).toEqual([
			["A", "zz-test-loop", "gpt-5.4-mini"],
			["B", "zz-test-loop", "gpt-5.4-mini"],
		]);
	});

	it("falls back to Its score for an unknown mode and says so", () => {
		const { draft, badMode } = draftFromLink({ agent: "codex", mode: "duel" }, { model: "gpt-5.6-terra" });
		expect(draft.mode).toBe("score");
		expect(badMode).toBe("duel");
	});

	it("takes only the linked benchmark and the linked agent's own model", () => {
		const { draft } = draftFromLink({ agent: "claude-code", suite: "zz-test-mcp" }, { model: "claude-sonnet-4-5" });
		expect(draft.benchmarks).toEqual(["zz-test-mcp"]);
		expect(draft.arms).toEqual([{ label: "A", agent: "claude-code", model: "claude-sonnet-4-5" }]);
	});

	it("sets the opponent only in opponent mode", () => {
		const o = draftFromLink({ agent: "codex", mode: "opponent", opponent: "claude-code", suite: "zz-test-mcp" }, { model: "m" }).draft;
		expect(o.opponent).toBe("claude-code");
		expect(o.arms.map((a) => a.agent)).toEqual(["codex", "claude-code"]);
		expect(learnStepReady(o)).toBe(true);
		expect(draftFromLink({ agent: "codex", opponent: "claude-code" }, { model: "m" }).draft.opponent).toBeNull();
	});
});

describe("agentPickPatch", () => {
	it("gives the picked agent its own model, not the previous agent's", () => {
		const old = applyPatch(EMPTY_DRAFT, { agent: "zz-test-loop", arms: [{ label: "A", agent: "zz-test-loop", model: "gpt-5.4-mini" }] });
		const d = applyPatch(old, agentPickPatch(old, { id: "claude-code", model: "claude-sonnet-4-5" }));
		expect(d.agent).toBe("claude-code");
		expect(d.version).toBeNull();
		expect(d.arms).toEqual([{ label: "A", agent: "claude-code", model: "claude-sonnet-4-5" }]);
	});
});

describe("learnStepReady with the latest version (N95)", () => {
	const v = (version: number | null, compareVersion: number | null) =>
		applyPatch(EMPTY_DRAFT, { agent: "zz-test-loop", mode: "versions", version, compareVersion });
	it("treats Latest as the highest version", () => {
		expect(learnStepReady(v(null, 2), 2)).toBe(false);
		expect(learnStepReady(v(null, 1), 2)).toBe(true);
		expect(learnStepReady(v(1, 2), 2)).toBe(true);
		expect(learnStepReady(v(2, 2), 2)).toBe(false);
	});
});