import { describe, expect, it } from "vitest";
import { BARE_MODEL_AGENT, EMPTY_DRAFT, applyPatch, defaultName, draftToSpec, specToPatch, withBareModel } from "./evaluateDraft";

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

	it("names a score evaluation with faults a failure test", () => {
		expect(defaultName({ ...d, version: 3, benchmarks: ["hd-harbor-bfcl", "b2"], faults: ["clean", "model-down"] })).toBe(
			"codex v3 failure test on hd-harbor-bfcl and 1 more",
		);
		expect(defaultName({ ...d, benchmarks: ["hd-harbor-bfcl"], faults: ["clean"] })).toBe("codex on hd-harbor-bfcl");
	});
});