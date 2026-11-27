import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it } from "vitest";
import { providerChoices } from "@/components/models/providerCatalog";
import { evaluationLabel, providerList, setupOptionLabel } from "./modelWatchForm";

const ev = {
	id: "ev_core",
	created_at: "2026-11-22T10:00:00Z",
	spec: { name: "codex score", arms: [{ label: "A", agent: "codex", model: "gpt-6.2" }, { label: "B", agent: "codex", model: "" }] },
} as unknown as Evaluation;

describe("Model watch form (lane 7c)", () => {
	it("names each evaluation with its date and models, so equal names differ", () => {
		expect(evaluationLabel(ev)).toBe("codex score, 22 Nov, gpt-6.2");
		expect(evaluationLabel({ ...ev, created_at: "" })).toBe("codex score, gpt-6.2");
	});

	it("shows a setup with its model", () => {
		expect(setupOptionLabel(ev, "A")).toBe("A: gpt-6.2");
		expect(setupOptionLabel(ev, "B")).toBe("B");
	});

	it("reads the picked providers without repeats, and offers every Bifrost provider by name", () => {
		expect(providerList("Anthropic, openai anthropic")).toEqual(["anthropic", "openai"]);
		const choices = providerChoices();
		expect(choices.find((c) => c.id === "anthropic")?.name).toBe("Anthropic");
		expect(choices.map((c) => c.name)).toEqual([...choices.map((c) => c.name)].sort((a, b) => a.localeCompare(b)));
	});
});
