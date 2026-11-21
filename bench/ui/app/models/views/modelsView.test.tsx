import "@/lib/i18n";

import { modelSuggestions, providersFor } from "@/components/models/modelPicker";
import { errorQuery, okQuery, render } from "@/lib/testing/render";
import type { ModelsCatalogResponse } from "@/lib/types/models";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ModelsView, { addParam, parseModels } from "./modelsView";

const api = vi.hoisted(() => ({ q: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/modelsApi", async () => {
	const { mutation } = await import("@/lib/testing/render");
	return {
		useGetModelsQuery: () => api.q.models,
		useCreateModelKeyMutation: () => mutation(),
		useUpdateModelKeyMutation: () => mutation(),
		useDeleteModelKeyMutation: () => mutation(),
		useTestModelKeyMutation: () => mutation(),
	};
});

const CATALOG: ModelsCatalogResponse = {
	keys_ready: true,
	providers: [
		{ id: "openai", name: "OpenAI", needs_base_url: false, key_hint: "platform.openai.com", models: [
			{ id: "gpt-4o", purposes: ["agent", "judge", "simuser"], rank: 80 },
			{ id: "gpt-4o-mini", cheap: true, purposes: ["agent", "judge", "simuser"], rank: 20 },
			{ id: "whisper-1", purposes: ["voice-stt"], rank: 30 },
		] },
		{ id: "anthropic", name: "Anthropic", needs_base_url: false, key_hint: "console", models: [{ id: "claude-haiku-4-5", purposes: ["agent"], rank: 50 }] },
		{ id: "openai-compatible", name: "OpenAI-compatible", needs_base_url: true, key_hint: "any", models: [] },
	],
	keys: [
		{ id: "key_1", provider: "openai", label: "team", last4: "abcd", status: "ok", models: [], budget_usd: 10, month_spent_usd: 12,
			over_budget: true, created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-01T00:00:00Z" },
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
		expect(html).toContain("works");
		expect(html).toContain("over budget");
		expect(html).toContain("gpt-4o-mini");
		expect(html).toContain('data-testid="model-key-test-key_1"');
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

describe("helpers", () => {
	it("parses ?add and model lists", () => {
		expect(addParam({ add: "groq" })).toBe("groq");
		expect(addParam({ add: "nope" })).toBeNull();
		expect(parseModels("a, b\nc,,")).toEqual(["a", "b", "c"]);
	});

	it("filters providers by purpose and sorts suggestions cheapest first", () => {
		expect(providersFor("voice-stt", CATALOG.providers)).toEqual(["openai"]);
		expect(providersFor("agent", CATALOG.providers)).toEqual(["openai", "anthropic", "openai-compatible"]);
		expect(modelSuggestions("openai", "agent", CATALOG.providers)).toEqual(["gpt-4o-mini", "gpt-4o"]);
	});
});
