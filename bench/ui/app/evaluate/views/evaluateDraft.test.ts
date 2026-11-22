import { describe, expect, it } from "vitest";
import { BARE_MODEL_AGENT, EMPTY_DRAFT, draftToSpec, specToPatch, withBareModel } from "./evaluateDraft";

describe("draftToSpec", () => {
	it("starts from the Quick look size and carries concurrency and preset", () => {
		const spec = draftToSpec({ ...EMPTY_DRAFT, agent: "codex", arms: [{ label: "A", agent: "codex", model: "m" }], benchmarks: ["x"], concurrency: 8 });
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
