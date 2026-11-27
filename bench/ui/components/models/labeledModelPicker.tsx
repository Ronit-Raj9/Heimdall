import {
	duplicateIds,
	groupModels,
	keyStateFor,
	optionLabel,
	preferNative,
	providerOfModel,
	type CatalogProvider,
	type ModelGroup,
	type GroupedModelPickerProps,
	type KeyState,
	type PickedModel,
} from "@/components/models/groupedModelPicker";
import { Combobox, type ComboboxItem } from "@/components/ui/combobox";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "@/components/runs/uf5bI18n";

/**
 * A provider/model pair picked on an authoring page (fix wave 3, N238, N267, N275, N276, N278):
 * the shared GroupedModelPicker under a visible label, and the key state of the picked provider,
 * so a button that starts paid work can wait for a model whose key works.
 */

/** "openai/gpt-5" as a picked model (split at the first "/"); null for "" or a ref without a provider. */
export function splitModelRef(ref: string | undefined | null): PickedModel | null {
	const s = (ref ?? "").trim();
	const i = s.indexOf("/");
	if (i <= 0 || i === s.length - 1) return null;
	return { provider: s.slice(0, i), model: s.slice(i + 1) };
}

/** Whether both halves of a picked model are set. */
export function isPicked(v: PickedModel | null | undefined): v is PickedModel {
	return !!v && v.provider.trim() !== "" && v.model.trim() !== "";
}

/** The key state of the picked model's provider, "none" when nothing is picked or the list has not loaded. */
export function usePickedKeyState(v: PickedModel | null | undefined): KeyState {
	const models = useGetModelsQuery();
	if (!isPicked(v) || !models.data) return "none";
	return keyStateFor(v.provider, models.data.keys ?? []);
}

/** Value of the "type another model id" item of a provider. */
const OTHER = "\u0001other:";

/**
 * The searchable list of every model, grouped by provider with its key state in the group name,
 * plus one "Other <provider> model" item per provider (M5-67, M4-86). Pure.
 */
export function modelComboItems(
	groups: readonly ModelGroup[],
	t: (key: "picker.other" | "picker.noKey" | "picker.keyNotWorking", o: { name: string }) => string,
): ComboboxItem[] {
	const dup = duplicateIds(groups);
	const items: ComboboxItem[] = [];
	for (const g of groups) {
		const group = g.keyState === "ok" ? g.name : g.keyState === "invalid" ? t("picker.keyNotWorking", { name: g.name }) : t("picker.noKey", { name: g.name });
		for (const m of g.models) items.push({ value: `${g.provider}\u0000${m.id}`, label: optionLabel(m, g, dup.has(m.id)), group, hint: m.id });
		items.push({ value: `${OTHER}${g.provider}`, label: t("picker.other", { name: g.name }), group });
	}
	return items;
}

/** The picked model of a combobox value; for an "Other" item, the provider with an empty model. Pure. */
export function pickedOfValue(v: string): PickedModel {
	if (v.startsWith(OTHER)) return { provider: v.slice(OTHER.length), model: "" };
	const [provider, model = ""] = v.split("\u0000");
	return { provider, model };
}

export interface LabeledModelPickerProps extends GroupedModelPickerProps {
	label: string;
	/** Shown under the picker, for example why a button is still disabled. */
	hint?: string;
}

/** The searchable model combobox under a visible label, with an optional hint line and a model id field for "Other". */
export function LabeledModelPicker({ label, hint, testId, value, onChange, disabled: disabledProp, providers }: LabeledModelPickerProps) {
	const { t } = useTranslation("uf5b");
	const tid = testId ?? "grouped-model-picker";
	const models = useGetModelsQuery();
	const catalog = (models.data?.providers ?? []) as unknown as CatalogProvider[];
	const keys = models.data?.keys ?? [];
	const [typing, setTyping] = useState<string | null>(null);
	const all = preferNative(groupModels(catalog, keys), catalog).filter((g) => !providers || providers.includes(g.provider));
	const noProvider = providers !== undefined && providers.length === 0;
	const disabled = disabledProp || noProvider;
	const model = value?.model ?? "";
	const provider = value?.provider || providerOfModel(all, model);
	const listed = all.some((g) => g.provider === provider && g.models.some((m) => m.id === model));
	const customFor = typing ?? (models.data && model !== "" && provider !== "" && !listed ? provider : null);
	const items = modelComboItems(all, t);
	const selected = customFor ? `${OTHER}${customFor}` : model !== "" ? `${provider}\u0000${model}` : null;
	const keyState = provider ? keyStateFor(provider, keys) : "ok";
	const providerName = catalog.find((p) => p.id === provider)?.name ?? provider;
	return (
		<div className="flex flex-col gap-1 text-sm" data-testid={testId ? `${testId}-field` : undefined}>
			<Combobox
				label={label}
				items={items}
				value={selected}
				onChange={(v) => {
					if (v === null) return;
					const picked = pickedOfValue(v);
					setTyping(v.startsWith(OTHER) ? picked.provider : null);
					onChange(picked);
				}}
				placeholder={noProvider ? t("picker.pickAgentFirst") : t("picker.choose")}
				query={models}
				onRetry={() => void models.refetch()}
				disabled={disabled}
				testId={tid}
			/>
			{customFor && (
				<input
					aria-label={t("picker.modelId")}
					className="border-input bg-background h-9 w-full min-w-0 rounded-md border px-3 text-sm sm:max-w-80"
					placeholder={t("picker.modelId")}
					value={model}
					disabled={disabled}
					onChange={(e) => onChange({ provider: customFor, model: e.target.value })}
					data-testid={`${tid}-custom`}
				/>
			)}
			{models.data && provider !== "" && keyState !== "ok" && (
				<p className="text-chart-warning-ink text-xs" data-testid={`${tid}-no-key`}>
					{keyState === "invalid" ? t("picker.keyBroken", { name: providerName }) : t("picker.keyNone", { name: providerName })}{" "}
					<Link to="/models" search={{ add: provider } as never} className="text-primary underline">
						{t("picker.addKey")}
					</Link>
				</p>
			)}
			{hint && (
				<p className="text-muted-foreground text-xs" data-testid={testId ? `${testId}-hint` : undefined}>
					{hint}
				</p>
			)}
		</div>
	);
}
