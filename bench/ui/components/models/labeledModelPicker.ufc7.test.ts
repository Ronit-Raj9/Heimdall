import { describe, expect, it } from "vitest";
import { modelComboItems, pickedOfValue } from "./labeledModelPicker";

const t = (key: string, o: { name: string }) => `${key}:${o.name}`;

describe("modelComboItems", () => {
	it("lists every model under its provider, with an Other item and the key state in the group", () => {
		const items = modelComboItems(
			[
				{ provider: "openai", name: "OpenAI", keyState: "ok", models: [{ id: "gpt-5", open: false }] },
				{ provider: "groq", name: "Groq", keyState: "none", models: [{ id: "llama", open: true }] },
			],
			t,
		);
		expect(items.map((i) => i.group)).toEqual(["OpenAI", "OpenAI", "picker.noKey:Groq", "picker.noKey:Groq"]);
		expect(pickedOfValue(items[0].value)).toEqual({ provider: "openai", model: "gpt-5" });
		expect(pickedOfValue(items[1].value)).toEqual({ provider: "openai", model: "" });
	});
});
