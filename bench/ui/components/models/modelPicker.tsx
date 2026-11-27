import {
	familyOf,
	groupProviders,
	isOpenWeights,
	keyUsable,
	OPEN_FAMILIES,
	providerName,
	type ProviderGroup,
} from "@/components/models/providerCatalog";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import type { ModelKey, ModelProviderInfo, ModelPurpose } from "@/lib/types/models";
import { Link } from "@tanstack/react-router";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import "@/components/reportcard/reportCardI18n";

/** Providers the UI knew before the server listed every Bifrost provider. */
export type KnownProviderId = "openai" | "anthropic" | "gemini" | "openrouter" | "groq" | "sarvam" | "openai-compatible";

/** A provider id from GET /models providers[]: any Bifrost provider or a custom one. */
export type ProviderId = KnownProviderId | (string & {});

export interface ModelRef {
	provider: ProviderId;
	model: string;
	keyId?: string;
}

export interface ModelPickerProps {
	value: ModelRef | null;
	onChange(v: ModelRef): void;
	purpose?: "agent" | "judge" | "simuser" | "voice-stt" | "voice-tts";
	allowedProviders?: ProviderId[];
	disabled?: boolean;
	showAddKeyLink?: boolean /*default true -> /models?add=<provider>*/;
	testId?: string;
}

/** Fallback provider list while the catalog has not loaded. */
export const PROVIDERS: readonly ProviderId[] = ["openai", "anthropic", "gemini", "openrouter", "groq", "sarvam", "openai-compatible"];

/** Purposes that call a chat model, where a provider's full model list is worth offering. */
const CHAT_PURPOSES: readonly ModelPurpose[] = ["agent", "judge", "simuser"];

/**
 * Which providers a purpose can use: every provider the catalog lists (or the allowed ones).
 * Speech purposes only list providers with a speech model; a provider with no suggested models
 * (custom endpoints, self-hosted servers) serves chat purposes only.
 */
export function providersFor(purpose: ModelPurpose, catalog: ModelProviderInfo[], allowed?: readonly ProviderId[]): ProviderId[] {
	if (catalog.length === 0) return [...(allowed ?? PROVIDERS)];
	const base = allowed ?? catalog.map((p) => p.id);
	return base.filter((id) => {
		const info = catalog.find((p) => p.id === id);
		if (!info) return false;
		const models = info.models ?? [];
		if (id === "openai-compatible" || models.length === 0) return CHAT_PURPOSES.includes(purpose);
		return models.some((m) => m.purposes.includes(purpose));
	});
}

/**
 * Model ids that cannot serve a chat loop: speech, transcription, image, embedding, moderation,
 * realtime and search models. The server's chatModelID (handlers/models_catalog.go) uses the same
 * pattern; keep the two in step.
 */
const NON_CHAT_RE =
	/(^|[-_/.:])(tts|transcribe|whisper|embed|embedding|image|dall-e|moderation|realtime|audio|speech|sora|search-preview)([-_/.:]|$)/i;

/**
 * Whether a model id can serve a chat loop (agent, judge or simulated user). A model the catalog
 * lists for the provider counts when one of its purposes is a chat purpose; any other id counts
 * unless its name says it is a speech, image, embedding or similar model.
 */
export function isChatModelId(id: string, catalog: ModelProviderInfo[], provider: ProviderId): boolean {
	const listed = catalog.find((p) => p.id === provider)?.models?.find((m) => m.id === id);
	if (listed) return listed.purposes.some((p) => CHAT_PURPOSES.includes(p));
	return !NON_CHAT_RE.test(id) && !id.toLowerCase().startsWith("chatgpt-image");
}

/** Suggested model ids for a provider and purpose: the key's own model list first, else the catalog, cheapest first. */
export function modelSuggestions(provider: ProviderId, purpose: ModelPurpose, catalog: ModelProviderInfo[], key?: ModelKey): string[] {
	if (key && key.models.length > 0) return key.models;
	const info = catalog.find((p) => p.id === provider);
	return (info?.models ?? [])
		.filter((m) => m.purposes.includes(purpose))
		.sort((a, b) => a.rank - b.rank)
		.map((m) => m.id);
}

export interface ModelChoices {
	/** Model ids the org's keys reported or allow, in the order the keys give them. */
	reported: string[];
	/** The provider's suggested models for the purpose that the keys did not already list. */
	suggested: string[];
}

/**
 * The model dropdown's options for a provider: first what the org's keys reported (a key's own
 * allowed models, then the chat ids its provider listed at the last successful test), then the
 * catalog's suggestions. With key set only that key counts; invalid keys never do.
 */
export function modelChoices(
	provider: ProviderId,
	purpose: ModelPurpose,
	catalog: ModelProviderInfo[],
	keys: ModelKey[],
	key?: ModelKey,
): ModelChoices {
	const usable = (key ? [key] : keys.filter((k) => k.provider === provider)).filter((k) => k.status !== "invalid");
	const seen = new Set<string>();
	const reported: string[] = [];
	const add = (list: string[], m: string) => {
		if (m && !seen.has(m)) {
			seen.add(m);
			list.push(m);
		}
	};
	for (const k of usable) for (const m of k.models) add(reported, m);
	// A provider's raw listing also carries speech, image and embedding models; a chat list keeps chat models only.
	if (CHAT_PURPOSES.includes(purpose))
		for (const k of usable) for (const m of k.models_available ?? []) if (isChatModelId(m, catalog, provider)) add(reported, m);
	const suggested: string[] = [];
	for (const m of modelSuggestions(provider, purpose, catalog)) add(suggested, m);
	return { reported, suggested };
}

/** The model a provider switch lands on: a key's allowed model, else the cheapest suggestion, else the first listed. */
function firstChoice(c: ModelChoices, key?: ModelKey): string {
	return key?.models[0] ?? c.suggested[0] ?? c.reported[0] ?? "";
}

const selectClass = "border-input bg-background h-8 rounded-md border px-2 text-sm disabled:opacity-50";
const OTHER = "__other__";
const SEP = "::";

/** True when the catalog marks (or the id suggests) open weights for this model of this provider. */
export function modelIsOpen(provider: ProviderId, model: string, catalog: ModelProviderInfo[]): boolean {
	const info = catalog.find((p) => p.id === provider)?.models?.find((m) => m.id === model);
	return isOpenWeights(info ?? { id: model });
}

export interface SearchHit {
	provider: ProviderId;
	model: string;
	open: boolean;
}

/**
 * Models across providers that match every word of the query. A word matches the model id, the
 * provider id or name, or the model's open family name; the word "open" matches open-weight models only.
 */
export function searchModels(
	query: string,
	providers: readonly ProviderId[],
	purpose: ModelPurpose,
	catalog: ModelProviderInfo[],
	keys: ModelKey[],
): SearchHit[] {
	const words = query.toLowerCase().split(/\s+/).filter(Boolean);
	if (words.length === 0) return [];
	const hits: SearchHit[] = [];
	for (const p of providers) {
		const c = modelChoices(p, purpose, catalog, keys);
		const pname = providerName(p, catalog).toLowerCase();
		for (const model of [...c.reported, ...c.suggested]) {
			const info = catalog.find((x) => x.id === p)?.models?.find((m) => m.id === model) ?? { id: model };
			const open = isOpenWeights(info);
			const fam = familyOf(info);
			const famName = OPEN_FAMILIES.find((f) => f.id === fam)?.name.toLowerCase() ?? "";
			const hay = `${model.toLowerCase()} ${p} ${pname} ${famName}`;
			if (words.every((w) => (w === "open" ? open : hay.includes(w)))) hits.push({ provider: p, model, open });
		}
	}
	return hits;
}

/**
 * Picks a provider, a model and (when the org has several) the key to use. A search box finds a
 * model across every provider, grouped by provider, with open-weight models tagged "open".
 * Providers are grouped (frontier APIs, open model hosts, cloud, self-hosted, custom); once any
 * provider has a working key, providers without one are disabled with a hint to add a key. With
 * no value yet it adopts the server's default (the cheapest model a working key can call). The
 * model is a dropdown of what the org's keys reported and the provider's suggestions; "Other
 * model..." reveals a text box for any other id.
 */
export function ModelPicker({
	value,
	onChange,
	purpose = "agent",
	allowedProviders,
	disabled,
	showAddKeyLink = true,
	testId,
}: ModelPickerProps) {
	const { t } = useTranslation("models");
	const { t: tu } = useTranslation("uf3");
	const id = useId();
	const tid = testId ?? "model-picker";
	const models = useGetModelsQuery();
	const loaded = Boolean(models.data);
	const catalog = models.data?.providers ?? [];
	const keys = models.data?.keys ?? [];
	const providers = providersFor(purpose, catalog, allowedProviders);
	const keyed = new Set(keys.filter(keyUsable).map((k) => k.provider));
	const broken = new Set(keys.filter((k) => !keyUsable(k)).map((k) => k.provider));
	const ordered = [...providers].sort((a, b) => Number(keyed.has(b)) - Number(keyed.has(a)));
	const provider: ProviderId = value?.provider ?? ordered[0] ?? "openai";
	const providerKeys = keys.filter((k) => k.provider === provider);
	const key = providerKeys.find((k) => k.id === value?.keyId);
	const choices = modelChoices(provider, purpose, catalog, keys, key);
	const listed = [...choices.reported, ...choices.suggested];
	const def = models.data?.default;
	const model = value?.model ?? "";
	const [otherPicked, setOtherPicked] = useState(false);
	const [query, setQuery] = useState("");
	// A model the lists do not carry is a custom one once the catalog has loaded; while it loads the
	// current id is shown as its own option so the select never looks empty.
	const other = otherPicked || (loaded && model !== "" && !listed.includes(model));
	const current = !other && model !== "" && !listed.includes(model) ? model : null;
	// Only lock providers out once some provider works, so a new org can still look around.
	const lockNoKey = loaded && keyed.size > 0;
	const blocked = (p: ProviderId) => lockNoKey && !keyed.has(p) && p !== provider;

	// Adopt the server default once, when nothing is picked yet.
	useEffect(() => {
		if (value || disabled || !def) return;
		if (!providers.includes(def.provider)) return;
		if (purpose !== "agent" && purpose !== "judge" && purpose !== "simuser") return;
		onChange({ provider: def.provider, model: def.model, keyId: def.key_id });
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [def?.provider, def?.model, def?.key_id, value === null]);

	const setProvider = (p: ProviderId) => {
		const first = keys.find((k) => k.provider === p && keyUsable(k));
		setOtherPicked(false);
		onChange({ provider: p, model: firstChoice(modelChoices(p, purpose, catalog, keys, first), first), keyId: first?.id });
	};

	const setModel = (m: string) => {
		if (m === OTHER) {
			setOtherPicked(true);
			if (listed.includes(model)) onChange({ provider, model: "", keyId: value?.keyId });
			return;
		}
		setOtherPicked(false);
		onChange({ provider, model: m, keyId: value?.keyId });
	};

	const pickHit = (v: string) => {
		const at = v.indexOf(SEP);
		if (at < 0) return;
		const p = v.slice(0, at);
		const m = v.slice(at + SEP.length);
		const first = keys.find((k) => k.provider === p && keyUsable(k));
		setOtherPicked(false);
		setQuery("");
		onChange({ provider: p, model: m, keyId: p === provider ? value?.keyId : first?.id });
	};

	const providerLabel = (p: ProviderId) => {
		const name = providerName(p, catalog);
		if (keyed.has(p) || !loaded) return name;
		return `${name} (${broken.has(p) ? t("picker.keyInvalid") : t("picker.noKeyShort")})`;
	};
	const modelLabel = (m: string) => (modelIsOpen(provider, m, catalog) ? `${m} (${t("picker.openTag")})` : m);

	const grouped = groupProviders(ordered.map((p) => ({ id: p, custom_base_url: catalog.find((c) => c.id === p)?.custom_base_url })));
	const groupLabel = (g: ProviderGroup) => t(`groups.${g}`);
	const hits = query.trim() ? searchModels(query, ordered, purpose, catalog, keys) : [];
	const hitGroups = ordered.map((p) => ({ provider: p, hits: hits.filter((h) => h.provider === p) })).filter((g) => g.hits.length > 0);

	return (
		<div className="flex flex-col gap-2" data-testid={tid}>
			<div className="flex flex-wrap items-center gap-2">
				<label className="sr-only" htmlFor={`${id}-search`}>
					{t("picker.search")}
				</label>
				<input
					id={`${id}-search`}
					type="search"
					autoComplete="off"
					className={`${selectClass} min-w-40`}
					value={query}
					disabled={disabled}
					placeholder={t("picker.searchPlaceholder")}
					onChange={(e) => setQuery(e.target.value)}
					data-testid={`${tid}-search`}
				/>
				<label className="sr-only" htmlFor={`${id}-provider`}>
					{t("picker.provider")}
				</label>
				<select
					id={`${id}-provider`}
					className={selectClass}
					value={provider}
					disabled={disabled}
					onChange={(e) => setProvider(e.target.value)}
					data-testid={`${tid}-provider`}
				>
					{!ordered.includes(provider) && <option value={provider}>{providerLabel(provider)}</option>}
					{grouped.length > 1
						? grouped.map((g) => (
								<optgroup key={g.group} label={groupLabel(g.group)}>
									{g.providers.map(({ id: p }) => (
										<option key={p} value={p} disabled={blocked(p)}>
											{providerLabel(p)}
										</option>
									))}
								</optgroup>
							))
						: ordered.map((p) => (
								<option key={p} value={p} disabled={blocked(p)}>
									{providerLabel(p)}
								</option>
							))}
				</select>
				<label className="sr-only" htmlFor={`${id}-model`}>
					{t("picker.model")}
				</label>
				<select
					id={`${id}-model`}
					className={`${selectClass} max-w-72 min-w-48`}
					value={other ? OTHER : model}
					disabled={disabled}
					onChange={(e) => setModel(e.target.value)}
					data-testid={`${tid}-model`}
				>
					{model === "" && !other && (
						<option value="" disabled>
							{t("picker.choose")}
						</option>
					)}
					{current && (
						<optgroup label={t("picker.current")}>
							<option value={current}>{current}</option>
						</optgroup>
					)}
					{choices.reported.length > 0 && (
						<optgroup label={t("picker.fromKeys")}>
							{choices.reported.map((m) => (
								<option key={m} value={m}>
									{modelLabel(m)}
								</option>
							))}
						</optgroup>
					)}
					{choices.suggested.length > 0 && (
						<optgroup label={t("picker.suggested")}>
							{choices.suggested.map((m) => (
								<option key={m} value={m}>
									{modelLabel(m)}
								</option>
							))}
						</optgroup>
					)}
					<option value={OTHER}>{t("picker.other")}</option>
				</select>
				{other && (
					<>
						<label className="sr-only" htmlFor={`${id}-model-other`}>
							{t("picker.custom")}
						</label>
						<input
							id={`${id}-model-other`}
							className={`${selectClass} min-w-48`}
							value={model}
							disabled={disabled}
							placeholder={t("picker.custom")}
							onChange={(e) => onChange({ provider, model: e.target.value, keyId: value?.keyId })}
							data-testid={`${tid}-model-other`}
						/>
					</>
				)}
				{providerKeys.length > 1 && (
					<select
						className={selectClass}
						aria-label={t("picker.key")}
						value={value?.keyId ?? ""}
						disabled={disabled}
						onChange={(e) => onChange({ provider, model, keyId: e.target.value || undefined })}
						data-testid={`${tid}-key`}
					>
						<option value="">{t("picker.anyKey")}</option>
						{providerKeys.map((k) => (
							<option key={k.id} value={k.id}>
								{k.label || k.provider} {k.last4 ? `...${k.last4}` : ""}
							</option>
						))}
					</select>
				)}
				{/* The key status is always shown, not only when it is missing (M3-27). */}
				{showAddKeyLink && keyed.has(provider) && loaded && providerKeys[0] && (
					<span className="text-muted-foreground text-xs" data-testid={`${tid}-key-ok`}>
						{tu("picker.keyOk", { label: providerKeys[0].label || providerKeys[0].provider, last4: providerKeys[0].last4 ?? "" })}
					</span>
				)}
				{showAddKeyLink && !keyed.has(provider) && loaded && (
					// Settings, Model keys, with the provider set and a way back to this page (M8-12).
					<Link
						to={"/settings" as never}
						search={{ tab: "keys", add: provider, return: typeof window === "undefined" ? undefined : window.location.pathname + window.location.search } as never}
						className="text-primary text-xs underline"
						data-testid={`${tid}-add-key`}
					>
						{providerKeys.length === 0 ? t("picker.noKey") : t("picker.keyInvalidLong")}: {t("picker.addKey")}
					</Link>
				)}
			</div>
			{query.trim() !== "" &&
				(hitGroups.length > 0 ? (
					<select
						className="border-input bg-background max-w-full rounded-md border p-1 text-sm"
						size={Math.min(10, hits.length + hitGroups.length)}
						aria-label={t("picker.results")}
						value=""
						disabled={disabled}
						onChange={(e) => pickHit(e.target.value)}
						data-testid={`${tid}-results`}
					>
						{hitGroups.map((g) => (
							<optgroup
								key={g.provider}
								label={
									blocked(g.provider) ? `${providerName(g.provider, catalog)} (${t("picker.needsKey")})` : providerName(g.provider, catalog)
								}
							>
								{g.hits.map((h) => (
									<option key={h.model} value={`${h.provider}${SEP}${h.model}`} disabled={blocked(h.provider)}>
										{h.open ? `${h.model} (${t("picker.openTag")})` : h.model}
									</option>
								))}
							</optgroup>
						))}
					</select>
				) : (
					<p className="text-muted-foreground text-xs" data-testid={`${tid}-no-results`}>
						{t("picker.noResults")}
					</p>
				))}
		</div>
	);
}

export default ModelPicker;