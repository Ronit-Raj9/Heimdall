import { modelChoices, type ProviderId } from "@/components/models/modelPicker";
import { Badge } from "@/components/ui/badge";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import type { ModelKey, ModelOption, ModelProviderInfo } from "@/lib/types/models";
import { KeyMissingNote } from "@/components/models/keyMissingNote";
import { useRouterState } from "@tanstack/react-router";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import "@/components/runs/uf5bI18n";
import "@/components/runs/ufc4I18n";
import { InlineError } from "@/components/runs/screenStates";
import { optionsState } from "@/lib/hooks/useOptionsState";

/**
 * A single searchable model dropdown grouped by provider, built on ModelPicker's modelChoices.
 * Unlike ModelPicker it lists every provider /models returns (any Bifrost provider, not only the
 * seven ModelPicker knows), tags open-weight models and warns when the org has no working key.
 */

/** Catalog fields added by the agent-first contract (section 5); optional until the server sends them. */
export type CatalogModel = ModelOption & { open_weights?: boolean; family?: string };
export type CatalogProvider = Omit<ModelProviderInfo, "id" | "models"> & {
	id: string;
	models: CatalogModel[];
	bifrost_provider?: string;
	custom_base_url?: boolean;
};

export type KeyState = "ok" | "invalid" | "none";

export interface ModelGroupItem {
	id: string;
	open: boolean;
	family?: string;
}

export interface ModelGroup {
	provider: string;
	name: string;
	keyState: KeyState;
	models: ModelGroupItem[];
}

export interface PickedModel {
	provider: string;
	model: string;
}

/** Whether the org has a working key for provider: "ok" when any key is not invalid. */
export function keyStateFor(provider: string, keys: ModelKey[]): KeyState {
	const mine = keys.filter((k) => k.provider === provider);
	if (mine.length === 0) return "none";
	return mine.some((k) => k.status !== "invalid") ? "ok" : "invalid";
}

/**
 * Provider groups for the dropdown: providers with a working key first, then the catalog order.
 * Each group lists what the keys reported, then the catalog's agent models. query filters by
 * model id, provider id or name, family, or the word "open" for open-weight models; a provider
 * whose name matches keeps all its models.
 */
export function groupModels(catalog: CatalogProvider[], keys: ModelKey[], query = ""): ModelGroup[] {
	const q = query.trim().toLowerCase();
	const groups: ModelGroup[] = catalog.map((p) => {
		const byId = new Map(p.models.map((m) => [m.id, m]));
		const c = modelChoices(p.id as ProviderId, "agent", catalog as unknown as ModelProviderInfo[], keys);
		const ids = [...c.reported, ...c.suggested];
		const models = ids.map((id) => {
			const m = byId.get(id);
			return { id, open: Boolean(m?.open_weights), family: m?.family };
		});
		return { provider: p.id, name: p.name || p.id, keyState: keyStateFor(p.id, keys), models };
	});
	const filtered = q
		? groups
				.map((g) => {
					if (g.provider.toLowerCase().includes(q) || g.name.toLowerCase().includes(q)) return g;
					const models = g.models.filter(
						(m) => m.id.toLowerCase().includes(q) || (m.family ?? "").toLowerCase().includes(q) || (q === "open" && m.open),
					);
					return { ...g, models };
				})
				.filter((g) => g.models.length > 0 || g.provider.toLowerCase().includes(q) || g.name.toLowerCase().includes(q))
		: groups;
	const rank = (s: KeyState) => (s === "ok" ? 0 : 1);
	return filtered
		.map((g, i) => ({ g, i }))
		.sort((a, b) => rank(a.g.keyState) - rank(b.g.keyState) || a.i - b.i)
		.map(({ g }) => g);
}

/** Model ids that two or more provider groups list (the same name under OpenAI and an OpenAI-compatible endpoint). */
export function duplicateIds(groups: readonly Pick<ModelGroup, "models">[]): Set<string> {
	const count = new Map<string, number>();
	for (const g of groups) for (const id of new Set(g.models.map((m) => m.id))) count.set(id, (count.get(id) ?? 0) + 1);
	return new Set([...count].filter(([, n]) => n > 1).map(([id]) => id));
}

/**
 * The text of one model option: the id, then the provider name when another provider lists the
 * same id (a closed select shows only the option text, not its group), then "[open]".
 */
export function optionLabel(m: ModelGroupItem, g: Pick<ModelGroup, "name">, dup: boolean): string {
	return `${m.id}${dup ? ` (${g.name})` : ""}${m.open ? "  [open]" : ""}`;
}

/** True for a provider reached through a custom base URL (OpenAI-compatible endpoints): it only mirrors native ids. */
function isCompatible(p: Pick<CatalogProvider, "id" | "custom_base_url" | "needs_base_url">): boolean {
	return Boolean(p.custom_base_url) || p.needs_base_url === true || p.id === "openai-compatible";
}

/**
 * The same model id under a native provider and an OpenAI-compatible endpoint is listed once, under
 * the native provider (M1-21). Groups keep their "Other ... model" entry even when emptied.
 */
export function preferNative(groups: readonly ModelGroup[], catalog: readonly CatalogProvider[]): ModelGroup[] {
	const compat = new Set(catalog.filter(isCompatible).map((p) => p.id));
	const native = new Set(groups.filter((g) => !compat.has(g.provider)).flatMap((g) => g.models.map((m) => m.id)));
	return groups.map((g) => (compat.has(g.provider) ? { ...g, models: g.models.filter((m) => !native.has(m.id)) } : g));
}

/**
 * The groups the closed list shows (M1-21): providers with a working key, plus the picked one. The
 * rest wait behind "More providers (needs a key)", except while searching or when no key works at all.
 */
export function visibleGroups(groups: readonly ModelGroup[], picked: string, showAll: boolean): { shown: ModelGroup[]; hidden: number } {
	if (showAll || !groups.some((g) => g.keyState === "ok")) return { shown: [...groups], hidden: 0 };
	const shown = groups.filter((g) => g.keyState === "ok" || g.provider === picked);
	return { shown, hidden: groups.length - shown.length };
}

const OTHER = "__other__:";
const MORE = "__more__";
const selectClass = "border-input bg-background h-8 rounded-md border px-2 text-sm disabled:opacity-50";

export interface GroupedModelPickerProps {
	value: PickedModel | null;
	onChange(v: PickedModel): void;
	disabled?: boolean;
	testId?: string;
	/**
	 * Only these providers' groups are listed (a fallback model must stay on the agent's provider).
	 * An empty list means the provider is not known yet: the picker is disabled and says so.
	 */
	providers?: readonly string[];
	/** The name of the model select for screen readers and tests (fix wave 3, N189); "Model" by default. */
	label?: string;
}

/** The provider of the first group that lists model (one with a working key first), or "". */
export function providerOfModel(groups: readonly Pick<ModelGroup, "provider" | "keyState" | "models">[], model: string): string {
	if (!model) return "";
	const carrying = groups.filter((g) => g.models.some((m) => m.id === model));
	return (carrying.find((g) => g.keyState === "ok") ?? carrying[0])?.provider ?? "";
}

/** The label of a picked model the list does not carry: "Provider: model", or the model id alone. */
export function modelOptionLabel(providerName: string, model: string): string {
	return providerName ? `${providerName}: ${model}` : model;
}

export function GroupedModelPicker({
	value,
	onChange,
	disabled: disabledProp,
	testId,
	providers,
	label,
}: GroupedModelPickerProps) {
	const { t } = useTranslation("uf5b");
	const { t: tc } = useTranslation("ufc4");
	const id = useId();
	const name = label ?? t("picker.model");
	const [showAll, setShowAll] = useState(false);
	const tid = testId ?? "grouped-model-picker";
	const models = useGetModelsQuery();
	const loaded = Boolean(models.data);
	// A failed load says so with a Retry, never a list stuck on "Loading" (M8-84).
	const failed = !loaded && optionsState(models, 0) === "error";
	const catalog = (models.data?.providers ?? []) as unknown as CatalogProvider[];
	const keys = models.data?.keys ?? [];
	const [query, setQuery] = useState("");
	const [typing, setTyping] = useState<string | null>(null);
	const only = (list: ModelGroup[]) => (providers ? list.filter((g) => providers.includes(g.provider)) : list);
	const all = only(preferNative(groupModels(catalog, keys, query), catalog));
	const dup = duplicateIds(only(preferNative(groupModels(catalog, keys), catalog)));
	const noProvider = providers !== undefined && providers.length === 0;
	const disabled = disabledProp || noProvider;
	const model = value?.model ?? "";
	// A model given without its provider (an agent's default model) takes the catalog provider that lists it.
	const provider = value?.provider || providerOfModel(groupModels(catalog, keys), model);
	const { shown: groups, hidden } = visibleGroups(all, provider, showAll || query.trim() !== "");
	const listed = groups.some((g) => g.provider === provider && g.models.some((m) => m.id === model));
	const pickedItem = groupModels(catalog, keys)
		.find((g) => g.provider === provider)
		?.models.find((m) => m.id === model);
	// A model no group carries is a custom id once the catalog has loaded.
	const customFor = typing ?? (loaded && model !== "" && provider !== "" && !pickedItem ? provider : null);
	const selected = customFor ? `${OTHER}${customFor}` : model !== "" ? `${provider}\u0000${model}` : "";
	const keyState = provider ? keyStateFor(provider, keys) : "ok";
	const providerName = catalog.find((p) => p.id === provider)?.name ?? provider;
	const returnTo = useRouterState({ select: (st) => st.location.href });

	const pick = (v: string) => {
		if (v === MORE) {
			setShowAll(true);
			return;
		}
		if (v.startsWith(OTHER)) {
			const p = v.slice(OTHER.length);
			setTyping(p);
			onChange({ provider: p, model: "" });
			return;
		}
		const [p, m] = v.split("\u0000");
		setTyping(null);
		onChange({ provider: p, model: m });
	};

	return (
		<div className="flex flex-col gap-1.5" data-testid={tid}>
			<div className="flex flex-wrap items-center gap-2">
				<label className="sr-only" htmlFor={`${id}-search`}>
					{label === undefined || label === t("picker.model") ? t("picker.search") : t("picker.searchFor", { label })}
				</label>
				<input
					id={`${id}-search`}
					className={`${selectClass} w-full sm:w-40`}
					placeholder={t("picker.search")}
					value={query}
					disabled={disabled}
					onChange={(e) => setQuery(e.target.value)}
					data-testid={`${tid}-search`}
				/>
				<label className="sr-only" htmlFor={`${id}-model`}>
					{name}
				</label>
				<select
					id={`${id}-model`}
					className={`${selectClass} w-full max-w-80 min-w-0 sm:w-auto sm:min-w-56`}
					value={selected}
					disabled={disabled}
					onChange={(e) => pick(e.target.value)}
					data-testid={`${tid}-model`}
				>
					<option value="" disabled>
						{noProvider ? t("picker.pickAgentFirst") : loaded ? t("picker.choose") : failed ? tc("picker.loadError") : t("picker.loading")}
					</option>
					{model !== "" && !listed && !customFor && <option value={selected}>{modelOptionLabel(providerName, model)}</option>}
					{groups.map((g) => (
						<optgroup
							key={g.provider}
							label={
								g.keyState === "ok"
									? g.name
									: g.keyState === "invalid"
										? t("picker.keyNotWorking", { name: g.name })
										: t("picker.noKey", { name: g.name })
							}
						>
							{g.models.map((m) => (
								<option key={m.id} value={`${g.provider}\u0000${m.id}`}>
									{optionLabel(m, g, dup.has(m.id))}
								</option>
							))}
							<option value={`${OTHER}${g.provider}`}>{t("picker.other", { name: g.name })}</option>
						</optgroup>
					))}
					{hidden > 0 && (
						<option value={MORE} data-testid={`${tid}-more`}>
							{t("picker.more")}
						</option>
					)}
				</select>
				{pickedItem?.open && (
					<Badge variant="neutral" data-testid={`${tid}-open`} title={t("picker.openTitle")}>
						{t("picker.open")}
					</Badge>
				)}
				{customFor && (
					<>
						<label className="sr-only" htmlFor={`${id}-custom`}>
							{t("picker.modelId")}
						</label>
						<input
							id={`${id}-custom`}
							className={`${selectClass} w-full min-w-0 sm:w-auto sm:min-w-48`}
							placeholder={t("picker.modelId")}
							value={model}
							disabled={disabled}
							onChange={(e) => onChange({ provider: customFor, model: e.target.value })}
							data-testid={`${tid}-custom`}
						/>
					</>
				)}
			</div>
			{failed && <InlineError error={models.error} title={tc("picker.loadError")} onRetry={() => void models.refetch()} testId={`${tid}-error`} />}
			{loaded && provider !== "" && keyState !== "ok" && (
				<div className="text-chart-warning-ink text-xs" data-testid={`${tid}-no-key`}>
					{/* Opens Settings, Model keys, on the Add panel for this provider and comes back here (M8-12). */}
					<KeyMissingNote provider={provider} providerName={providerName} returnTo={returnTo} className="flex flex-wrap items-center gap-2 text-xs" />
				</div>
			)}
		</div>
	);
}

export default GroupedModelPicker;