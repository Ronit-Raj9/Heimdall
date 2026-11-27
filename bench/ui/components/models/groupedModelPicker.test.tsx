import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import type { ModelKey } from "@/lib/types/models";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	type CatalogProvider,
	GroupedModelPicker,
	duplicateIds,
	groupModels,
	isMoreValue,
	optionLabel,
	pickerItems,
	preferNative,
	visibleGroups,
} from "./groupedModelPicker";

const q = vi.hoisted(() => ({ models: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/modelsApi", () => ({ useGetModelsQuery: () => q.models }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const catalog: CatalogProvider[] = [
	{ id: "openai", name: "OpenAI", needs_base_url: false, key_hint: "", models: [{ id: "gpt-4o-mini", purposes: ["agent"], rank: 1 }] },
	{
		id: "openai-compatible",
		name: "OpenAI-compatible endpoint",
		needs_base_url: true,
		key_hint: "",
		models: [{ id: "gpt-5.6-luna", purposes: ["agent"], rank: 1 }],
	},
	{ id: "groq", name: "Groq", needs_base_url: false, key_hint: "", models: [{ id: "llama-3-8b", purposes: ["agent"], rank: 1 }] },
];

const keys = [
	{
		id: "k1",
		provider: "openai",
		label: "",
		last4: "",
		status: "ok",
		models: [],
		models_available: ["gpt-5.6-luna"],
		budget_usd: 0,
		month_spent_usd: 0,
		over_budget: false,
		created_at: "",
		updated_at: "",
	},
] as unknown as ModelKey[];

// The picker is a Combobox: a closed list renders no options, so the tests read the item builder.
const tx = ((key: string, o?: { name: string }) =>
	({ "picker.other": `Other ${o?.name} model...`, "picker.noKey": `${o?.name} (no key)`, "picker.keyNotWorking": `${o?.name} (key not working)`, "picker.more": "More providers (needs a key)..." })[key] ?? key) as never;
const itemTexts = (providers?: string[], picked = { provider: "", model: "", custom: false, providerName: "" }, showAll = false) => {
	const all = preferNative(groupModels(catalog, keys), catalog).filter((g) => !providers || providers.includes(g.provider));
	return pickerItems(all, picked, showAll, tx).map((i) => i.label);
};

beforeEach(() => {
	q.models = okQuery({ providers: catalog, keys });
});

describe("duplicate model ids (N88, M1-21)", () => {
	it("finds an id two providers list, and the picker keeps it only under the native provider", () => {
		const groups = groupModels(catalog, keys);
		const dup = duplicateIds(groups);
		expect([...dup]).toEqual(["gpt-5.6-luna"]);
		const openai = groups.find((g) => g.provider === "openai")!;
		expect(optionLabel({ id: "gpt-5.6-luna", open: false }, openai, true)).toBe("gpt-5.6-luna (OpenAI)");
		expect(optionLabel({ id: "llama-3-8b", open: true }, openai, false)).toBe("llama-3-8b  [open]");
		const native = preferNative(groups, catalog);
		expect(native.find((g) => g.provider === "openai-compatible")!.models.map((m) => m.id)).toEqual([]);
		expect(duplicateIds(native).size).toBe(0);
		const texts = itemTexts();
		expect(texts).toContain("gpt-5.6-luna");
		expect(texts).not.toContain("gpt-5.6-luna (OpenAI-compatible endpoint)");
		expect(texts).toContain("gpt-4o-mini");
	});
	it("puts providers without a key behind More providers, except the picked one (M1-21)", () => {
		const groups = preferNative(groupModels(catalog, keys), catalog);
		expect(visibleGroups(groups, "", false).shown.map((g) => g.provider)).toEqual(["openai"]);
		expect(visibleGroups(groups, "groq", false).shown.map((g) => g.provider)).toEqual(["openai", "groq"]);
		expect(visibleGroups(groups, "", true).hidden).toBe(0);
		const texts = itemTexts();
		expect(texts).not.toContain("llama-3-8b");
		expect(texts).toContain("More providers (needs a key)...");
		expect(itemTexts(undefined, undefined, true)).toContain("llama-3-8b");
		expect(isMoreValue(pickerItems(preferNative(groupModels(catalog, keys), catalog), { provider: "", model: "", custom: false, providerName: "" }, false, tx).at(-1)!.value)).toBe(true);
	});
	it("lists a picked model no group carries on its own row", () => {
		expect(itemTexts(undefined, { provider: "openai", model: "gpt-9", custom: false, providerName: "OpenAI" })[0]).toBe("OpenAI: gpt-9");
	});
});

describe("providers prop (N89)", () => {
	it("names the model select with the label it is given, Model by default (N189)", () => {
		const named = render(<GroupedModelPicker value={null} onChange={() => undefined} label="Setup A (codex) model" />);
		expect(named).toMatch(/<label[^>]*for="[^"]+-input"[^>]*>Setup A \(codex\) model<\/label>/);
		expect(named).toContain('role="combobox"');
		expect(render(<GroupedModelPicker value={null} onChange={() => undefined} />)).toMatch(
			/<label[^>]*for="[^"]+-input"[^>]*>Model<\/label>/,
		);
	});

	it("lists only the given providers", () => {
		const texts = itemTexts(["openai"]);
		expect(texts).toContain("gpt-4o-mini");
		expect(texts.join(" ")).not.toContain("Groq");
		expect(texts.join(" ")).not.toContain("OpenAI-compatible endpoint");
		expect(texts).toContain("Other OpenAI model...");
	});
	it("is disabled with a hint when the provider is not known yet", () => {
		const html = render(<GroupedModelPicker value={null} onChange={() => undefined} providers={[]} />);
		expect(html).toContain("Pick the agent&#x27;s model first");
		expect(html).toMatch(/role="combobox"[^>]*disabled=""/);
		expect(html).not.toContain("Groq");
	});
});
describe("failed load (M8-84)", () => {
	it("says the list could not load, with Retry, instead of Loading", () => {
		q.models = { isError: true, error: { status: 500 }, refetch: () => undefined };
		const html = render(<GroupedModelPicker value={null} onChange={() => undefined} />);
		expect(html).toContain("Could not load the list of models.");
		expect(html).toContain('data-testid="grouped-model-picker-error"');
	});
});
describe("missing key (M8-12)", () => {
	it("shows KeyMissingNote with a way back to the page it was given", () => {
		const html = render(<GroupedModelPicker value={{ provider: "groq", model: "llama-3-8b" }} onChange={() => undefined} returnTo="/evaluate?step=4" />);
		expect(html).toContain('data-testid="grouped-model-picker-no-key"');
		expect(html).toContain("No working Groq key.");
		expect(html).toContain('href="/settings?tab=keys&amp;add=groq&amp;return=%2Fevaluate%3Fstep%3D4"');
		expect(html).not.toContain("/models?add=");
	});
});
