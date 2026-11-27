import { describe, expect, it } from "vitest";
import { modelComboItems, type ModelGroup } from "./groupedModelPicker";

describe("model list (Q-73)", () => {
	it("lists a model once even when its provider repeats it", () => {
		const g: ModelGroup = { provider: "openai", name: "OpenAI", keyState: "ok", models: [{ id: "gpt-4o", open: false }, { id: "gpt-4o", open: false }] };
		const items = modelComboItems([g], (_k, o) => o.name);
		expect(items.filter((i) => i.hint === "gpt-4o")).toHaveLength(1);
	});
});
