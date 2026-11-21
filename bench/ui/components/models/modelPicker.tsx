import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import type { ModelKey, ModelProviderInfo, ModelPurpose } from "@/lib/types/models";
import { Link } from "@tanstack/react-router";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";

export type ProviderId = "openai" | "anthropic" | "gemini" | "openrouter" | "groq" | "sarvam" | "openai-compatible";

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

export const PROVIDERS: readonly ProviderId[] = ["openai", "anthropic", "gemini", "openrouter", "groq", "sarvam", "openai-compatible"];

/** Which providers a purpose can use: speech purposes only list providers with a speech model. */
export function providersFor(purpose: ModelPurpose, catalog: ModelProviderInfo[], allowed?: readonly ProviderId[]): ProviderId[] {
	const base = allowed ?? PROVIDERS;
	if (catalog.length === 0) return [...base];
	return base.filter((id) => {
		const info = catalog.find((p) => p.id === id);
		if (!info) return false;
		if (id === "openai-compatible") return purpose === "agent" || purpose === "judge" || purpose === "simuser";
		return info.models.some((m) => m.purposes.includes(purpose));
	});
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

/** Purposes that call a chat model, where a provider's full model list is worth offering. */
const CHAT_PURPOSES: readonly ModelPurpose[] = ["agent", "judge", "simuser"];

export interface ModelChoices {
	/** Model ids the org's keys reported or allow, in the order the keys give them. */
	reported: string[];
	/** The provider's suggested models for the purpose that the keys did not already list. */
	suggested: string[];
}

/**
 * The model dropdown's options for a provider: first what the org's keys reported (a key's own
 * allowed models, then the ids its provider listed at the last successful test), then the
 * catalog's suggestions. With key set only that key counts; invalid keys never do.
 */
export function modelChoices(provider: ProviderId, purpose: ModelPurpose, catalog: ModelProviderInfo[], keys: ModelKey[], key?: ModelKey): ModelChoices {
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
	if (CHAT_PURPOSES.includes(purpose)) for (const k of usable) for (const m of k.models_available ?? []) add(reported, m);
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

/**
 * Picks a provider, a model and (when the org has several) the key to use. Providers with a usable
 * key come first; with no value yet it adopts the server's default (the cheapest model a working
 * key can call). The model is a dropdown of what the org's keys reported and the provider's
 * suggestions; "Other model..." reveals a text box for any other id.
 */
export function ModelPicker({ value, onChange, purpose = "agent", allowedProviders, disabled, showAddKeyLink = true, testId }: ModelPickerProps) {
	const { t } = useTranslation("models");
	const id = useId();
	const tid = testId ?? "model-picker";
	const models = useGetModelsQuery();
	const loaded = Boolean(models.data);
	const catalog = models.data?.providers ?? [];
	const keys = models.data?.keys ?? [];
	const providers = providersFor(purpose, catalog, allowedProviders);
	const keyed = new Set(keys.filter((k) => k.status !== "invalid").map((k) => k.provider));
	const broken = new Set(keys.filter((k) => k.status === "invalid").map((k) => k.provider));
	const ordered = [...providers].sort((a, b) => Number(keyed.has(b)) - Number(keyed.has(a)));
	const provider: ProviderId = value?.provider ?? ordered[0] ?? "openai";
	const providerKeys = keys.filter((k) => k.provider === provider);
	const key = providerKeys.find((k) => k.id === value?.keyId);
	const choices = modelChoices(provider, purpose, catalog, keys, key);
	const listed = [...choices.reported, ...choices.suggested];
	const def = models.data?.default;
	const model = value?.model ?? "";
	const [otherPicked, setOtherPicked] = useState(false);
	// A model the lists do not carry is a custom one once the catalog has loaded; while it loads the
	// current id is shown as its own option so the select never looks empty.
	const other = otherPicked || (loaded && model !== "" && !listed.includes(model));
	const current = !other && model !== "" && !listed.includes(model) ? model : null;

	// Adopt the server default once, when nothing is picked yet.
	useEffect(() => {
		if (value || disabled || !def) return;
		if (!providers.includes(def.provider)) return;
		if (purpose !== "agent" && purpose !== "judge" && purpose !== "simuser") return;
		onChange({ provider: def.provider, model: def.model, keyId: def.key_id });
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [def?.provider, def?.model, def?.key_id, value === null]);

	const setProvider = (p: ProviderId) => {
		const first = keys.find((k) => k.provider === p && k.status !== "invalid");
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

	const providerLabel = (p: ProviderId) => {
		const name = catalog.find((c) => c.id === p)?.name ?? p;
		if (keyed.has(p) || !loaded) return name;
		return `${name} (${broken.has(p) ? t("picker.keyInvalid") : t("picker.noKeyShort")})`;
	};

	return (
		<div className="flex flex-wrap items-center gap-2" data-testid={tid}>
			<label className="sr-only" htmlFor={`${id}-provider`}>
				{t("picker.provider")}
			</label>
			<select
				id={`${id}-provider`}
				className={selectClass}
				value={provider}
				disabled={disabled}
				onChange={(e) => setProvider(e.target.value as ProviderId)}
				data-testid={`${tid}-provider`}
			>
				{ordered.map((p) => (
					<option key={p} value={p}>
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
								{m}
							</option>
						))}
					</optgroup>
				)}
				{choices.suggested.length > 0 && (
					<optgroup label={t("picker.suggested")}>
						{choices.suggested.map((m) => (
							<option key={m} value={m}>
								{m}
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
			{showAddKeyLink && providerKeys.length === 0 && (
				<Link to="/models" search={{ add: provider } as never} className="text-primary text-xs underline" data-testid={`${tid}-add-key`}>
					{t("picker.noKey")}: {t("picker.addKey")}
				</Link>
			)}
		</div>
	);
}

export default ModelPicker;
