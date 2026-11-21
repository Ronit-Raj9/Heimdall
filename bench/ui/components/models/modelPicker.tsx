import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import type { ModelKey, ModelProviderInfo, ModelPurpose } from "@/lib/types/models";
import { Link } from "@tanstack/react-router";
import { useEffect, useId } from "react";
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

const selectClass = "border-input bg-background h-8 rounded-md border px-2 text-sm disabled:opacity-50";

/**
 * Picks a provider, a model and (when the org has several) the key to use. Providers with a usable
 * key come first; with no value yet it adopts the server's default (the cheapest model a working
 * key can call). Any model id can be typed: the list only suggests.
 */
export function ModelPicker({ value, onChange, purpose = "agent", allowedProviders, disabled, showAddKeyLink = true, testId }: ModelPickerProps) {
	const { t } = useTranslation("models");
	const id = useId();
	const models = useGetModelsQuery();
	const catalog = models.data?.providers ?? [];
	const keys = models.data?.keys ?? [];
	const providers = providersFor(purpose, catalog, allowedProviders);
	const keyed = new Set(keys.filter((k) => k.status !== "invalid").map((k) => k.provider));
	const ordered = [...providers].sort((a, b) => Number(keyed.has(b)) - Number(keyed.has(a)));
	const provider: ProviderId = value?.provider ?? ordered[0] ?? "openai";
	const providerKeys = keys.filter((k) => k.provider === provider);
	const key = providerKeys.find((k) => k.id === value?.keyId);
	const suggestions = modelSuggestions(provider, purpose, catalog, key ?? providerKeys[0]);
	const def = models.data?.default;

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
		onChange({ provider: p, model: modelSuggestions(p, purpose, catalog, first)[0] ?? "", keyId: first?.id });
	};

	return (
		<div className="flex flex-wrap items-center gap-2" data-testid={testId ?? "model-picker"}>
			<label className="sr-only" htmlFor={`${id}-provider`}>
				{t("picker.provider")}
			</label>
			<select
				id={`${id}-provider`}
				className={selectClass}
				value={provider}
				disabled={disabled}
				onChange={(e) => setProvider(e.target.value as ProviderId)}
				data-testid={`${testId ?? "model-picker"}-provider`}
			>
				{ordered.map((p) => (
					<option key={p} value={p}>
						{catalog.find((c) => c.id === p)?.name ?? p}
						{keyed.has(p) ? "" : " (no key)"}
					</option>
				))}
			</select>
			<label className="sr-only" htmlFor={`${id}-model`}>
				{t("picker.model")}
			</label>
			<input
				id={`${id}-model`}
				list={`${id}-models`}
				className={`${selectClass} min-w-48`}
				value={value?.model ?? ""}
				disabled={disabled}
				placeholder={suggestions[0] ?? t("picker.custom")}
				onChange={(e) => onChange({ provider, model: e.target.value, keyId: value?.keyId })}
				data-testid={`${testId ?? "model-picker"}-model`}
			/>
			<datalist id={`${id}-models`}>
				{suggestions.map((m) => (
					<option key={m} value={m} />
				))}
			</datalist>
			{providerKeys.length > 1 && (
				<select
					className={selectClass}
					aria-label={t("picker.key")}
					value={value?.keyId ?? ""}
					disabled={disabled}
					onChange={(e) => onChange({ provider, model: value?.model ?? "", keyId: e.target.value || undefined })}
					data-testid={`${testId ?? "model-picker"}-key`}
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
				<Link
					to="/models"
					search={{ add: provider } as never}
					className="text-primary text-xs underline"
					data-testid={`${testId ?? "model-picker"}-add-key`}
				>
					{t("picker.noKey")}: {t("picker.addKey")}
				</Link>
			)}
		</div>
	);
}

export default ModelPicker;
