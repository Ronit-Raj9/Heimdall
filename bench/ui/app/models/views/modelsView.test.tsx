import "@/lib/i18n";

import { modelSuggestions, providersFor } from "@/components/models/modelPicker";
import { routerState } from "@/lib/testing/routerMock";
import { errorQuery, okQuery, render, tagOf } from "@/lib/testing/render";
import type { ModelsCatalogResponse } from "@/lib/types/models";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { familyServing, isOpenWeights, providerGroup } from "@/components/models/providerCatalog";
import ModelsView, {
	addParam,
	credentialValue,
	filterProviders,
	keyPrice,
	keyRequestBody,
	missingCredentialFields,
	needsBaseUrl,
	parseModels,
	addModel,
} from "./modelsView";

const api = vi.hoisted(() => ({ q: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/components/models/serveOpenModel", () => ({ ServeOpenModel: () => null })); // Z05: its own test (serveOpenModel.test.tsx)
vi.mock("@/lib/store/apis/modelsApi", async () => {
	const { mutation } = await import("@/lib/testing/render");
	return {
		useGetModelsQuery: () => api.q.models,
		useGetModelProvidersQuery: () => api.q.providers ?? { data: undefined },
		useCreateModelKeyMutation: () => mutation(),
		useUpdateModelKeyMutation: () => mutation(),
		useDeleteModelKeyMutation: () => mutation(),
		useTestModelKeyMutation: () => mutation(),
	};
});

const CATALOG: ModelsCatalogResponse = {
	keys_ready: true,
	providers: [
		{
			id: "openai",
			name: "OpenAI",
			needs_base_url: false,
			key_hint: "platform.openai.com",
			models: [
				{ id: "gpt-4o", purposes: ["agent", "judge", "simuser"], rank: 80 },
				{ id: "gpt-4o-mini", cheap: true, purposes: ["agent", "judge", "simuser"], rank: 20 },
				{ id: "whisper-1", purposes: ["voice-stt"], rank: 30 },
			],
		},
		{
			id: "anthropic",
			name: "Anthropic",
			needs_base_url: false,
			key_hint: "console",
			models: [{ id: "claude-haiku-4-5", purposes: ["agent"], rank: 50 }],
		},
		{ id: "openai-compatible", name: "OpenAI-compatible", needs_base_url: true, key_hint: "any", models: [] },
		{
			id: "deepseek",
			name: "DeepSeek",
			needs_base_url: false,
			key_hint: "",
			models: [{ id: "deepseek-chat", purposes: ["agent"], rank: 10, open_weights: true, family: "deepseek" }],
		},
		{
			id: "openrouter",
			name: "OpenRouter",
			needs_base_url: false,
			key_hint: "",
			bifrost_provider: "openrouter",
			custom_base_url: false,
			models: [
				{ id: "qwen/qwen3-coder", purposes: ["agent"], rank: 10 },
				{ id: "moonshotai/kimi-k2", purposes: ["agent"], rank: 10 },
			],
		},
		{ id: "bedrock", name: "AWS Bedrock", needs_base_url: false, key_hint: "", models: [] },
		{ id: "vllm", name: "vLLM", needs_base_url: true, key_hint: "", models: [] },
		{
			id: "zhipu",
			name: "Zhipu (custom)",
			needs_base_url: false,
			key_hint: "",
			custom_base_url: true,
			models: [{ id: "glm-4.6", purposes: ["agent"], rank: 10 }],
		},
	],
	keys: [
		{
			id: "key_1",
			provider: "openai",
			label: "team",
			last4: "abcd",
			status: "ok",
			models: [],
			budget_usd: 10,
			month_spent_usd: 12,
			over_budget: true,
			created_at: "2026-11-01T00:00:00Z",
			updated_at: "2026-11-01T00:00:00Z",
		},
	],
	default: { provider: "openai", model: "gpt-4o-mini", key_id: "key_1" },
};

beforeEach(() => {
	api.q = { models: okQuery(CATALOG) };
});

describe("ModelsView", () => {
	it("lists keys with only the last 4 characters, status and budget", () => {
		const html = render(<ModelsView />);
		expect(html).toContain('data-testid="model-key-key_1"');
		expect(html).toContain("...abcd");
		expect(html).toContain("Works");
		expect(html).toContain("over budget");
		expect(html).toContain("gpt-4o-mini");
		expect(html).toContain('data-testid="model-key-test-key_1"');
	});

	it("groups every provider with add key, status and test", () => {
		const html = render(<ModelsView />);
		for (const g of ["frontier", "open-hosts", "cloud", "self-hosted", "custom"])
			expect(html).toContain(`data-testid="provider-group-${g}"`);
		expect(html).toContain("Frontier APIs");
		expect(html).toContain("Custom OpenAI-compatible");
		for (const p of ["openai", "deepseek", "openrouter", "bedrock", "vllm", "zhipu"])
			expect(html).toContain(`data-testid="provider-add-${p}"`);
		expect(html).toMatch(/data-testid="provider-status-openai"[^>]*>Over budget/);
		// A provider with no key shows "No keys yet" only, no second "No key" badge (M8-6).
		expect(html).not.toContain('data-testid="provider-status-bedrock"');
	});

	it("lists open model families and who serves them", () => {
		const html = render(<ModelsView />);
		expect(html).toContain('data-testid="open-models"');
		for (const f of ["deepseek", "glm", "kimi", "mimo", "minimax", "mistral", "qwen", "llama", "gpt-oss"])
			expect(html).toContain(`data-testid="open-family-${f}"`);
		expect(html).toContain('data-testid="open-family-deepseek-deepseek"');
		expect(html).toContain('data-testid="open-family-qwen-openrouter"');
		expect(html).toContain('data-testid="open-family-kimi-openrouter"');
		expect(html).toContain('data-testid="open-family-glm-zhipu"');
	});

	it("offers to add a key when there are none", () => {
		api.q = { models: okQuery({ ...CATALOG, keys: [], default: undefined }) };
		const html = render(<ModelsView />);
		expect(html).toContain('data-testid="models-empty"');
		expect(html).toContain("Add a model key");
	});

	it("says what to configure when the key store is off", () => {
		api.q = { models: okQuery({ ...CATALOG, keys_ready: false, keys: [] }) };
		expect(render(<ModelsView />)).toContain("HEIMDALL_MASTER_KEY");
	});

	it("shows a load error", () => {
		api.q = { models: errorQuery(500) };
		expect(render(<ModelsView />)).toContain('data-testid="models-error"');
	});
});

describe("key form credential fields (GET /models/providers)", () => {
	const providers = {
		providers: [
			{
				id: "bedrock",
				name: "AWS Bedrock",
				fields: [
					{ name: "access_key", label: "Access key id", required: true },
					{ name: "secret_key", label: "Secret access key", secret: true, required: true },
					{ name: "region", label: "Region", required: true },
				],
			},
			{ id: "ollama", name: "Ollama", keyless: true, needs_base_url: true },
		],
		custom: { id: "custom", name: "custom" },
	};
	it("shows one input per field for a structured credential, secrets hidden", () => {
		routerState.location.search = { add: "bedrock" };
		api.q = { ...api.q, providers: okQuery(providers) };
		try {
			const html = render(<ModelsView />);
			expect(html).toContain('data-testid="model-key-fields"');
			expect(tagOf(html, "model-key-field-secret_key")).toContain('type="password"');
			expect(tagOf(html, "model-key-field-region")).toContain('type="text"');
			expect(html).not.toContain('data-testid="model-key-value"');
		} finally {
			routerState.location.search = {};
			delete api.q.providers;
		}
	});
	it("builds the JSON value and lists the required fields left", () => {
		expect(credentialValue({ access_key: " AK ", secret_key: "SK", region: "" })).toBe('{"access_key":"AK","secret_key":"SK"}');
		expect(credentialValue({ region: " " })).toBe("");
		expect(missingCredentialFields(providers.providers[0], { access_key: "AK", region: "us-east-1" })).toEqual(["secret_key"]);
		expect(missingCredentialFields(undefined, {})).toEqual([]);
	});
});

describe("key form autofill guard", () => {
	it("never lets the browser fill the key value or label", () => {
		routerState.location.search = { add: "openai" };
		try {
			const html = render(<ModelsView />);
			const value = tagOf(html, "model-key-value") ?? "";
			expect(value).toContain('autoComplete="new-password"');
			expect(value).toContain('data-1p-ignore="true"');
			expect(value).toContain('data-lpignore="true"');
			const label = tagOf(html, "model-key-label") ?? "";
			expect(label).toContain('autoComplete="off"');
			expect(label).toContain('name="heimdall-key-label"');
			expect(label).toContain('data-lpignore="true"');
			expect(tagOf(html, "model-key-base-url")).toContain('autoComplete="off"');
		} finally {
			routerState.location.search = {};
		}
	});

	it("omits an empty value so an edit keeps the stored key", () => {
		const base = { provider: "openai" as const, label: " team ", baseUrl: "", modelsText: "a, b", budget: "5" };
		const edit = keyRequestBody({ ...base, value: "   " });
		expect("value" in edit).toBe(false);
		expect(edit).toEqual({ provider: "openai", label: "team", base_url: "", models: ["a", "b"], budget_usd: 5 });
		expect(keyRequestBody({ ...base, value: " sk-new " }).value).toBe("sk-new");
	});

	it("sends a per-key price only when one is typed", () => {
		const base = { provider: "openai" as const, label: "", baseUrl: "", modelsText: "", budget: "0", value: "" };
		expect(keyRequestBody({ ...base, price: { input: "0.5", output: " 2 ", cache: "" } }).price).toEqual({
			input_per_mtok: 0.5,
			output_per_mtok: 2,
			cache_per_mtok: 0,
		});
		expect("price" in keyRequestBody({ ...base, price: { input: "", output: "0", cache: "-1" } })).toBe(false);
		expect(keyPrice(undefined)).toBeUndefined();
	});
});

describe("helpers", () => {
	it("parses ?add and model lists", () => {
		expect(addParam({ add: "groq" })).toBe("groq");
		expect(addParam({ add: "nope" })).toBeNull();
		expect(addParam({ add: "bedrock" })).toBe("bedrock");
		expect(addParam({ add: "zhipu" }, ["openai", "zhipu"])).toBe("zhipu");
		expect(addParam({ add: "groq" }, ["openai"])).toBeNull();
		expect(parseModels("a, b\nc,,")).toEqual(["a", "b", "c"]);
	});

	it("filters providers by purpose and sorts suggestions cheapest first", () => {
		expect(providersFor("voice-stt", CATALOG.providers)).toEqual(["openai"]);
		expect(providersFor("agent", CATALOG.providers.slice(0, 3))).toEqual(["openai", "anthropic", "openai-compatible"]);
		expect(modelSuggestions("openai", "agent", CATALOG.providers)).toEqual(["gpt-4o-mini", "gpt-4o"]);
	});
});

describe("provider catalog", () => {
	it("groups providers, custom base URL ones under custom", () => {
		expect(providerGroup({ id: "anthropic" })).toBe("frontier");
		expect(providerGroup({ id: "fireworks" })).toBe("open-hosts");
		expect(providerGroup({ id: "vertex" })).toBe("cloud");
		expect(providerGroup({ id: "sgl" })).toBe("self-hosted");
		expect(providerGroup({ id: "moonshot", custom_base_url: true })).toBe("custom");
		expect(providerGroup({ id: "mystery" })).toBe("other");
		expect(needsBaseUrl({ needs_base_url: false, custom_base_url: true })).toBe(true);
		expect(needsBaseUrl({ needs_base_url: false })).toBe(false);
	});

	it("filters providers by name or group", () => {
		const label = (g: string) => (g === "cloud" ? "Cloud platforms" : g);
		expect(filterProviders(CATALOG.providers, "cloud", label).map((p) => p.id)).toEqual(["bedrock"]);
		expect(filterProviders(CATALOG.providers, "deep", label).map((p) => p.id)).toEqual(["deepseek"]);
	});

	it("uses the server's open_weights first, else guesses from the id", () => {
		expect(isOpenWeights({ id: "gpt-4o" })).toBe(false);
		expect(isOpenWeights({ id: "openai/gpt-oss-120b" })).toBe(true);
		expect(isOpenWeights({ id: "qwen-max" })).toBe(false);
		expect(isOpenWeights({ id: "custom-x", open_weights: true })).toBe(true);
		expect(isOpenWeights({ id: "deepseek-chat", open_weights: false })).toBe(false);
		const rows = familyServing(CATALOG.providers);
		expect(rows.find((r) => r.family.id === "mistral")?.providers).toEqual([]);
		expect(rows.find((r) => r.family.id === "qwen")?.providers.map((p) => p.models)).toEqual([["qwen/qwen3-coder"]]);
	});
});
describe("default model price hint (F1.price)", () => {
	const withDefault = (price?: NonNullable<ModelsCatalogResponse["default"]>["price"]) =>
		okQuery({ ...CATALOG, default: { provider: "openai" as const, model: "gpt-6.2-sol", key_id: "key_1", ...(price ? { price } : {}) } });
	const hint = (html: string) => html.match(/data-testid="models-price-hint"[^>]*>([^<]*)</)?.[1];

	it("names the key the price comes from", () => {
		api.q = {
			models: withDefault({ input_per_mtok: 2, output_per_mtok: 10, cache_per_mtok: 0.1, source: "key", key_label: "ronit-biforst" }),
		};
		expect(hint(render(<ModelsView />))).toBe("Price per 1M tokens: $2.00 in, $10.00 out, $0.10 cached (from your key ronit-biforst)");
	});

	it("names the price list", () => {
		api.q = { models: withDefault({ input_per_mtok: 0.15, output_per_mtok: 0.6, cache_per_mtok: 0.075, source: "price_list" }) };
		expect(hint(render(<ModelsView />))).toBe("Price per 1M tokens: $0.15 in, $0.60 out, $0.075 cached (from the price list)");
	});

	it("says not set only when nothing prices the model", () => {
		api.q = { models: withDefault() };
		expect(hint(render(<ModelsView />))).toBe("Price per 1M tokens: not set (costs will show as unknown)");
	});

	it("explains in the key form that an empty model list prices every model", () => {
		routerState.location.search = { add: "openai" };
		try {
			const html = render(<ModelsView />);
			const fieldset = html.slice(html.indexOf('data-testid="model-key-price"'), html.indexOf("</fieldset>"));
			expect(fieldset).toContain("With no models listed, this price applies to every model of this provider.");
			// The key form's own hint, not the default model's "not set" line (both used the key priceHint).
			expect(fieldset).toContain("What you pay on this key.");
			expect(fieldset).not.toContain("not set");
		} finally {
			routerState.location.search = {};
		}
	});
});

describe("addModel", () => {
	it("adds a model once", () => {
		expect(addModel("", "gpt-5")).toBe("gpt-5");
		expect(addModel("a, b", "c")).toBe("a, b, c");
		expect(addModel("a, b", "a")).toBe("a, b");
	});
});
