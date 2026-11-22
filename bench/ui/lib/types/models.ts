// Models and keys area (web parity L04): mirrors handlers/models.go.
import type { ProviderId } from "@/components/models/modelPicker";

export type ModelPurpose = "agent" | "judge" | "simuser" | "voice-stt" | "voice-tts";
export type ModelKeyStatus = "untested" | "ok" | "invalid" | "error";

export interface ModelKey {
	id: string;
	provider: ProviderId;
	label: string;
	base_url?: string;
	last4: string;
	status: ModelKeyStatus;
	models: string[];
	/** Model ids the provider listed at the key's last successful test (sorted, at most 200). */
	models_available?: string[];
	budget_usd: number;
	month_spent_usd: number;
	over_budget: boolean;
	created_by?: string;
	created_at: string;
	updated_at: string;
	last_tested_at?: string;
	/** What the org pays on this key per 1M tokens; prices runs of models the price list does not have. */
	price?: ModelKeyPrice;
}

/** A key's price per 1M tokens (USD). cache_per_mtok 0 prices cached tokens at the input price. */
export interface ModelKeyPrice {
	input_per_mtok: number;
	output_per_mtok: number;
	cache_per_mtok: number;
}

export interface ModelKeyRequest {
	provider: ProviderId;
	label?: string;
	base_url?: string;
	/** Plaintext key; sent once, never returned. Empty on update keeps the stored one. */
	value?: string;
	models?: string[];
	budget_usd?: number;
	/** Replaces the stored price; left out clears it. */
	price?: ModelKeyPrice;
}

export interface ModelOption {
	id: string;
	cheap?: boolean;
	purposes: ModelPurpose[];
	rank: number;
	/** True when the model's weights are published (DeepSeek, Qwen, Llama...). Optional: older servers omit it. */
	open_weights?: boolean;
	/** Model family id, e.g. "deepseek", "qwen", "glm". Optional. */
	family?: string;
}

export interface ModelProviderInfo {
	id: ProviderId;
	name: string;
	default_base_url?: string;
	needs_base_url: boolean;
	key_hint: string;
	key_prefix?: string;
	models: ModelOption[];
	/** The Bifrost core provider this entry routes through (e.g. "openrouter"). Optional. */
	bifrost_provider?: string;
	/** True for a custom OpenAI-compatible provider that needs the user's base URL. Optional. */
	custom_base_url?: boolean;
}

export interface ModelRefView {
	provider: ProviderId;
	model: string;
	key_id?: string;
	/** The model's price per 1M tokens and where it comes from; absent when neither a key nor the price list knows it. */
	price?: ModelPriceView;
}

/** A model's price per 1M tokens (USD): from one of the org's keys (key_label names it) or the server's price list. */
export interface ModelPriceView {
	input_per_mtok: number;
	output_per_mtok: number;
	cache_per_mtok: number;
	source: "key" | "price_list";
	key_label?: string;
}

export interface ModelsCatalogResponse {
	providers: ModelProviderInfo[];
	keys: ModelKey[];
	default?: ModelRefView;
	keys_ready: boolean;
}

export interface ModelKeyTestResult {
	ok: boolean;
	status: ModelKeyStatus;
	message: string;
	http_status?: number;
	latency_ms: number;
	models?: string[];
}

export interface ModelKeyTestResponse {
	key: ModelKey;
	result: ModelKeyTestResult;
}

// Types the "models" i18n namespace from its catalog (lib/i18n/resources.ts LaneResources).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		models: typeof import("@/locales/en/models.json");
	}
}
/** One credential field of a provider whose key is a JSON object (schemas.ProviderField). */
export interface ProviderCredentialField {
	name: string;
	label: string;
	secret?: boolean;
	required?: boolean;
	help?: string;
}

/** What the key form needs from a Bifrost provider (schemas.BifrostProviderSpec, GET /models/providers). */
export interface BifrostProviderSpec {
	id: string;
	name: string;
	route?: string;
	base_url?: string;
	test_base_url?: string;
	needs_base_url?: boolean;
	keyless?: boolean;
	fields?: ProviderCredentialField[];
	custom?: boolean;
	docs?: string;
	[key: string]: unknown;
}

/** GET /models/providers */
export interface BifrostProvidersResponse {
	providers: BifrostProviderSpec[];
	custom: BifrostProviderSpec;
}