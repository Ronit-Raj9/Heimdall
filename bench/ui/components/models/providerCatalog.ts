// Provider groups and open model families for the Models & keys page and the ModelPicker.
// The server's GET /models providers[] is the source of truth for which providers exist; this
// file only adds display data (group, short name) and infers open model families when the
// server does not send the optional open_weights / family fields yet.
import type { ModelKey, ModelOption, ModelProviderInfo } from "@/lib/types/models";

export type ProviderGroup = "frontier" | "open-hosts" | "cloud" | "self-hosted" | "custom" | "other";

export const PROVIDER_GROUPS: readonly ProviderGroup[] = ["frontier", "open-hosts", "cloud", "self-hosted", "custom", "other"];

/** Every provider Bifrost core supports, with its group and display name. */
export const BIFROST_PROVIDERS: Readonly<Record<string, { group: ProviderGroup; name: string }>> = {
	openai: { group: "frontier", name: "OpenAI" },
	anthropic: { group: "frontier", name: "Anthropic" },
	gemini: { group: "frontier", name: "Google Gemini" },
	xai: { group: "frontier", name: "xAI" },
	mistral: { group: "frontier", name: "Mistral" },
	deepseek: { group: "frontier", name: "DeepSeek" },
	minimax: { group: "frontier", name: "MiniMax" },
	cohere: { group: "frontier", name: "Cohere" },
	perplexity: { group: "frontier", name: "Perplexity" },
	sarvam: { group: "frontier", name: "Sarvam AI" },
	elevenlabs: { group: "frontier", name: "ElevenLabs" },
	"github-copilot": { group: "frontier", name: "GitHub Copilot" },
	openrouter: { group: "open-hosts", name: "OpenRouter" },
	groq: { group: "open-hosts", name: "Groq" },
	cerebras: { group: "open-hosts", name: "Cerebras" },
	fireworks: { group: "open-hosts", name: "Fireworks AI" },
	together: { group: "open-hosts", name: "Together AI" },
	huggingface: { group: "open-hosts", name: "Hugging Face" },
	nebius: { group: "open-hosts", name: "Nebius" },
	parasail: { group: "open-hosts", name: "Parasail" },
	baseten: { group: "open-hosts", name: "Baseten" },
	replicate: { group: "open-hosts", name: "Replicate" },
	azure: { group: "cloud", name: "Azure OpenAI" },
	bedrock: { group: "cloud", name: "AWS Bedrock" },
	vertex: { group: "cloud", name: "Google Vertex AI" },
	sagemaker: { group: "cloud", name: "AWS SageMaker" },
	databricks: { group: "cloud", name: "Databricks" },
	cloudflare: { group: "cloud", name: "Cloudflare Workers AI" },
	byteplus: { group: "cloud", name: "BytePlus" },
	ollama: { group: "self-hosted", name: "Ollama" },
	vllm: { group: "self-hosted", name: "vLLM" },
	sgl: { group: "self-hosted", name: "SGLang" },
	"openai-compatible": { group: "custom", name: "OpenAI-compatible endpoint" },
};

/** The group a provider belongs to. Custom base URL providers the server marks go to custom. */
export function providerGroup(p: Pick<ModelProviderInfo, "id"> & Partial<Pick<ModelProviderInfo, "custom_base_url">>): ProviderGroup {
	const known = BIFROST_PROVIDERS[p.id];
	if (known) return known.group;
	if (p.custom_base_url) return "custom";
	return "other";
}

/** Providers grouped in display order; groups with no providers are left out. */
export function groupProviders<T extends Pick<ModelProviderInfo, "id"> & Partial<Pick<ModelProviderInfo, "custom_base_url">>>(
	providers: readonly T[],
): { group: ProviderGroup; providers: T[] }[] {
	return PROVIDER_GROUPS.map((group) => ({ group, providers: providers.filter((p) => providerGroup(p) === group) })).filter(
		(g) => g.providers.length > 0,
	);
}

export function providerName(id: string, catalog: readonly Pick<ModelProviderInfo, "id" | "name">[]): string {
	return catalog.find((p) => p.id === id)?.name ?? BIFROST_PROVIDERS[id]?.name ?? id;
}

/** Up to two letters for a provider tile. */
export function providerInitials(id: string): string {
	const parts = id.split(/[^a-z0-9]+/i).filter(Boolean);
	if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
	return id.slice(0, 2).toUpperCase();
}

/** Open model families shown on the keys page, in display order. */
export type OpenFamily = "deepseek" | "glm" | "kimi" | "mimo" | "minimax" | "mistral" | "qwen" | "llama" | "gpt-oss";

export const OPEN_FAMILIES: readonly { id: OpenFamily; name: string; maker: string; nativeProvider?: string }[] = [
	{ id: "deepseek", name: "DeepSeek", maker: "DeepSeek", nativeProvider: "deepseek" },
	{ id: "glm", name: "GLM", maker: "Zhipu AI" },
	{ id: "kimi", name: "Kimi", maker: "Moonshot AI" },
	{ id: "mimo", name: "MiMo", maker: "Xiaomi" },
	{ id: "minimax", name: "MiniMax", maker: "MiniMax", nativeProvider: "minimax" },
	{ id: "mistral", name: "Mistral", maker: "Mistral AI", nativeProvider: "mistral" },
	{ id: "qwen", name: "Qwen", maker: "Alibaba" },
	{ id: "llama", name: "Llama", maker: "Meta" },
	{ id: "gpt-oss", name: "gpt-oss", maker: "OpenAI" },
];

const FAMILY_PATTERNS: readonly [OpenFamily, RegExp][] = [
	["gpt-oss", /gpt-oss/],
	["deepseek", /deepseek/],
	["glm", /(^|[/-])glm-?\d/],
	["kimi", /kimi|moonshot/],
	["mimo", /(^|[/-])mimo/],
	["minimax", /minimax/],
	["mistral", /mistral|mixtral|devstral|codestral|magistral|ministral|pixtral/],
	["qwen", /qwen|qwq/],
	["llama", /llama/],
];

/** Hosted-only models of open families (no published weights). */
const CLOSED_IDS = /qwen-(max|plus|turbo|flash)|mistral-(medium|large-latest)|codestral-latest|magistral-medium/;

/** The open family of a model: the server's family field first, else guessed from the id. */
export function familyOf(m: Pick<ModelOption, "id"> & Partial<Pick<ModelOption, "family">>): OpenFamily | string | undefined {
	if (m.family) return m.family.toLowerCase();
	const id = m.id.toLowerCase();
	for (const [fam, re] of FAMILY_PATTERNS) if (re.test(id)) return fam;
	return undefined;
}

/** Whether a model has open weights: the server's open_weights field first, else guessed from its family. */
export function isOpenWeights(m: Pick<ModelOption, "id"> & Partial<Pick<ModelOption, "family" | "open_weights">>): boolean {
	if (typeof m.open_weights === "boolean") return m.open_weights;
	if (CLOSED_IDS.test(m.id.toLowerCase())) return false;
	return familyOf(m) !== undefined;
}

export interface FamilyServing {
	family: (typeof OPEN_FAMILIES)[number];
	/** Providers in the catalog with at least one model of this family, and those model ids. */
	providers: { id: string; name: string; models: string[] }[];
}

/** For each open family, which catalog providers serve it. */
export function familyServing(catalog: readonly ModelProviderInfo[]): FamilyServing[] {
	return OPEN_FAMILIES.map((family) => ({
		family,
		providers: catalog
			.map((p) => ({ id: p.id, name: p.name, models: (p.models ?? []).filter((m) => familyOf(m) === family.id).map((m) => m.id) }))
			.filter((p) => p.models.length > 0 || p.id === family.nativeProvider),
	}));
}

/** A key can be used: not refused by the provider, reachable at the last test, under budget. */
export function keyUsable(k: Pick<ModelKey, "status" | "over_budget">): boolean {
	return k.status !== "invalid" && k.status !== "error" && !k.over_budget;
}

export type ProviderKeyState = "ok" | "untested" | "over-budget" | "failed" | "none";

/** The summary status of a provider's keys. */
export function providerKeyState(keys: readonly Pick<ModelKey, "status" | "over_budget">[]): ProviderKeyState {
	if (keys.length === 0) return "none";
	if (keys.some((k) => k.status === "ok" && !k.over_budget)) return "ok";
	if (keys.some(keyUsable)) return "untested";
	if (keys.some((k) => k.over_budget && k.status !== "invalid" && k.status !== "error")) return "over-budget";
	return "failed";
}
/** Every Bifrost provider as a choice for a picker, by display name. Pure. */
export function providerChoices(): { id: string; name: string }[] {
	return Object.entries(BIFROST_PROVIDERS)
		.map(([id, p]) => ({ id, name: p.name }))
		.sort((a, b) => a.name.localeCompare(b.name));
}
