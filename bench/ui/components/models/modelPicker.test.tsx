import "@/lib/i18n";

import { okQuery, loadingQuery, render, tagOf } from "@/lib/testing/render";
import type { ModelKey, ModelProviderInfo, ModelsCatalogResponse } from "@/lib/types/models";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ModelPicker, modelChoices, providersFor, searchModels } from "./modelPicker";

const q = vi.hoisted(() => ({ models: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/modelsApi", () => ({ useGetModelsQuery: () => q.models }));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));

const providers: ModelProviderInfo[] = [
	{
		id: "openai",
		name: "OpenAI",
		needs_base_url: false,
		key_hint: "",
		models: [
			{ id: "gpt-4o", purposes: ["agent"], rank: 2 },
			{ id: "gpt-4o-mini", purposes: ["agent"], rank: 1 },
			{ id: "whisper-1", purposes: ["voice-stt"], rank: 1 },
		],
	},
	{
		id: "anthropic",
		name: "Anthropic",
		needs_base_url: false,
		key_hint: "",
		models: [{ id: "claude-haiku", purposes: ["agent"], rank: 1 }],
	},
	{ id: "groq", name: "Groq", needs_base_url: false, key_hint: "", models: [{ id: "llama-3-8b", purposes: ["agent"], rank: 1 }] },
	{
		id: "deepseek",
		name: "DeepSeek",
		needs_base_url: false,
		key_hint: "",
		models: [
			{ id: "deepseek-chat", purposes: ["agent"], rank: 3, open_weights: true, family: "deepseek" },
			{ id: "deepseek-reasoner", purposes: ["agent"], rank: 4 },
		],
	},
	{ id: "ollama", name: "Ollama", needs_base_url: true, key_hint: "", models: [] },
] as unknown as ModelProviderInfo[];

const key = (over: Partial<ModelKey>): ModelKey => ({
	id: "k1",
	provider: "openai",
	label: "main",
	last4: "abcd",
	status: "ok",
	models: [],
	models_available: [],
	budget_usd: 0,
	month_spent_usd: 0,
	over_budget: false,
	created_at: "",
	updated_at: "",
	...over,
});

function catalog(keys: ModelKey[], extra: Partial<ModelsCatalogResponse> = {}) {
	q.models = okQuery({ providers, keys, keys_ready: true, ...extra });
}

const options = (html: string) => [...html.matchAll(/<option[^>]*value="([^"]*)"/g)].map((m) => m[1]);
const providerSelect = (html: string) =>
	html.slice(html.indexOf('data-testid="mp-provider"'), html.indexOf("</select>", html.indexOf('data-testid="mp-provider"')));
const modelSelect = (html: string) =>
	html.slice(html.indexOf('data-testid="mp-model"'), html.indexOf("</select>", html.indexOf('data-testid="mp-model"')));

beforeEach(() => {
	catalog([key({ models_available: ["gpt-4o", "gpt-5-preview", "o3"] }), key({ id: "k2", provider: "anthropic", status: "invalid" })]);
});

describe("modelChoices", () => {
	it("lists key-reported models first, then suggestions not already listed", () => {
		const keys = [key({ models: ["gpt-custom"], models_available: ["gpt-4o", "o3"] })];
		expect(modelChoices("openai", "agent", providers, keys)).toEqual({
			reported: ["gpt-custom", "gpt-4o", "o3"],
			suggested: ["gpt-4o-mini"],
		});
	});

	it("ignores invalid keys and listed models for speech purposes", () => {
		expect(modelChoices("openai", "agent", providers, [key({ status: "invalid", models_available: ["x"] })]).reported).toEqual([]);
		expect(modelChoices("openai", "voice-stt", providers, [key({ models_available: ["x"] })])).toEqual({
			reported: [],
			suggested: ["whisper-1"],
		});
	});

	it("uses only the chosen key when one is given", () => {
		const a = key({ models_available: ["a"] });
		const b = key({ id: "k2", models_available: ["b"] });
		expect(modelChoices("openai", "agent", providers, [a, b], b).reported).toEqual(["b"]);
		expect(modelChoices("openai", "agent", providers, [a, b]).reported).toEqual(["a", "b"]);
	});
});

describe("ModelPicker", () => {
	it("renders a model dropdown in order: key models, suggestions, Other", () => {
		const html = render(<ModelPicker value={{ provider: "openai", model: "o3", keyId: "k1" }} onChange={() => undefined} testId="mp" />);
		expect(tagOf(html, "mp-model")).toMatch(/^<select/);
		expect(options(modelSelect(html))).toEqual(["gpt-4o", "gpt-5-preview", "o3", "gpt-4o-mini", "__other__"]);
		expect(modelSelect(html)).toContain('<option value="o3" selected="">');
		expect(html).toContain("From your keys");
		expect(html).toContain("Other model...");
		expect(html).not.toContain('data-testid="mp-model-other"');
	});

	it("shows key status on the provider select", () => {
		const html = render(<ModelPicker value={{ provider: "openai", model: "gpt-4o" }} onChange={() => undefined} testId="mp" />);
		expect(html).toContain(">OpenAI</option>");
		expect(html).toContain("Anthropic (key not working)");
		expect(html).toContain("Groq (no key)");
		// Providers with a usable key come first.
		expect(options(providerSelect(html))[0]).toBe("openai");
	});

	it("groups providers and disables those without a working key", () => {
		const html = render(<ModelPicker value={{ provider: "openai", model: "gpt-4o" }} onChange={() => undefined} testId="mp" />);
		const sel = providerSelect(html);
		expect(sel).toContain('<optgroup label="Frontier APIs">');
		expect(sel).toContain('<optgroup label="Open-weight model hosts">');
		expect(sel).toContain('<optgroup label="Self-hosted (Ollama, vLLM, SGLang)">');
		expect(sel).toMatch(/<option value="groq" disabled="">Groq \(no key\)<\/option>/);
		expect(sel).toMatch(/<option value="openai"( selected="")?>OpenAI<\/option>/);
	});

	it("keeps every provider enabled when no key works yet", () => {
		catalog([]);
		const html = render(<ModelPicker value={{ provider: "openai", model: "gpt-4o" }} onChange={() => undefined} testId="mp" />);
		expect(providerSelect(html)).not.toContain('disabled=""');
	});

	it("tags open-weight models", () => {
		catalog([key({ provider: "deepseek" })]);
		const html = render(<ModelPicker value={{ provider: "deepseek", model: "deepseek-chat" }} onChange={() => undefined} testId="mp" />);
		expect(modelSelect(html)).toContain(">deepseek-chat (open)</option>");
		expect(modelSelect(html)).toContain(">deepseek-reasoner (open)</option>");
		expect(tagOf(html, "mp-search")).toContain('autoComplete="off"');
	});

	it("reveals the text box for a model the lists do not carry", () => {
		const html = render(<ModelPicker value={{ provider: "openai", model: "my-finetune" }} onChange={() => undefined} testId="mp" />);
		expect(modelSelect(html)).toContain('<option value="__other__" selected="">');
		expect(tagOf(html, "mp-model-other")).toContain('value="my-finetune"');
	});

	it("keeps the current model as an option while the catalog loads", () => {
		q.models = loadingQuery();
		const html = render(<ModelPicker value={{ provider: "openai", model: "gpt-x" }} onChange={() => undefined} testId="mp" />);
		expect(modelSelect(html)).toContain('<option value="gpt-x" selected="">');
		expect(html).not.toContain('data-testid="mp-model-other"');
	});

	it("asks to choose when nothing is picked and links to add a key", () => {
		const html = render(<ModelPicker value={{ provider: "groq", model: "" }} onChange={() => undefined} testId="mp" />);
		expect(html).toContain("Choose a model");
		expect(options(modelSelect(html))).toEqual(["", "llama-3-8b", "__other__"]);
		expect(html).toContain('data-testid="mp-add-key"');
	});

	it("keeps the default test ids and the key select", () => {
		catalog([key({}), key({ id: "k2", label: "backup" })]);
		const html = render(<ModelPicker value={{ provider: "openai", model: "gpt-4o" }} onChange={() => undefined} />);
		for (const id of ["model-picker", "model-picker-provider", "model-picker-model", "model-picker-key"])
			expect(html).toContain(`data-testid="${id}"`);
	});
});

describe("searchModels", () => {
	const keys = [key({}), key({ id: "k2", provider: "deepseek" })];
	const ids = ["openai", "anthropic", "groq", "deepseek", "ollama"];

	it("matches model ids, provider names and the open tag, grouped in provider order", () => {
		expect(searchModels("mini", ids, "agent", providers, keys).map((h) => `${h.provider}/${h.model}`)).toEqual(["openai/gpt-4o-mini"]);
		expect(searchModels("groq", ids, "agent", providers, keys).map((h) => h.model)).toEqual(["llama-3-8b"]);
		const open = searchModels("open", ids, "agent", providers, keys);
		expect(open.map((h) => h.model)).toEqual(["llama-3-8b", "deepseek-chat", "deepseek-reasoner"]);
		expect(open.every((h) => h.open)).toBe(true);
		expect(searchModels("deepseek chat", ids, "agent", providers, keys).map((h) => h.model)).toEqual(["deepseek-chat"]);
		expect(searchModels("  ", ids, "agent", providers, keys)).toEqual([]);
	});

	it("lists providers without suggested models for chat only", () => {
		expect(providersFor("agent", providers)).toContain("ollama");
		expect(providersFor("voice-stt", providers)).toEqual(["openai"]);
	});
});