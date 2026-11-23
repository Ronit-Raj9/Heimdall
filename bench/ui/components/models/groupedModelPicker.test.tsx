import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import type { ModelKey } from "@/lib/types/models";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { type CatalogProvider, GroupedModelPicker, duplicateIds, groupModels, optionLabel } from "./groupedModelPicker";

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

const optionTexts = (html: string) => [...html.matchAll(/<option[^>]*>([^<]*)<\/option>/g)].map((m) => m[1]);

beforeEach(() => {
	q.models = okQuery({ providers: catalog, keys });
});

describe("duplicate model ids (N88)", () => {
	it("finds an id two providers list and names the provider in each option", () => {
		const groups = groupModels(catalog, keys);
		const dup = duplicateIds(groups);
		expect([...dup]).toEqual(["gpt-5.6-luna"]);
		const openai = groups.find((g) => g.provider === "openai")!;
		expect(optionLabel({ id: "gpt-5.6-luna", open: false }, openai, true)).toBe("gpt-5.6-luna (OpenAI)");
		expect(optionLabel({ id: "llama-3-8b", open: true }, openai, false)).toBe("llama-3-8b  [open]");
		const texts = optionTexts(render(<GroupedModelPicker value={null} onChange={() => undefined} />));
		expect(texts).toContain("gpt-5.6-luna (OpenAI)");
		expect(texts).toContain("gpt-5.6-luna (OpenAI-compatible endpoint)");
		expect(texts).toContain("gpt-4o-mini");
		expect(texts).toContain("llama-3-8b");
	});
});

describe("providers prop (N89)", () => {
	it("names the model select with the label it is given, Model by default (N189)", () => {
		const named = render(<GroupedModelPicker value={null} onChange={() => undefined} label="Setup A (codex) model" />);
		expect(named).toMatch(/<label[^>]*for="[^"]+-model"[^>]*>Setup A \(codex\) model<\/label>/);
		expect(named).toContain("Search Setup A (codex) model");
		expect(render(<GroupedModelPicker value={null} onChange={() => undefined} />)).toMatch(
			/<label[^>]*for="[^"]+-model"[^>]*>Model<\/label>/,
		);
	});

	it("lists only the given providers", () => {
		const html = render(<GroupedModelPicker value={null} onChange={() => undefined} providers={["openai"]} />);
		expect(html).toContain('label="OpenAI"');
		expect(html).not.toContain("Groq");
		expect(html).not.toContain("OpenAI-compatible endpoint");
		expect(html).toContain("Other OpenAI model...");
	});
	it("is disabled with a hint when the provider is not known yet", () => {
		const html = render(<GroupedModelPicker value={null} onChange={() => undefined} providers={[]} />);
		expect(html).toContain("Pick the agent&#x27;s model first");
		expect(html).toMatch(/<select[^>]*disabled/);
		expect(html).not.toContain("Groq");
	});
});