import {
	GroupedModelPicker,
	keyStateFor,
	type GroupedModelPickerProps,
	type KeyState,
	type PickedModel,
} from "@/components/models/groupedModelPicker";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";

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

export { modelComboItems, pickedOfValue } from "@/components/models/groupedModelPicker";

export interface LabeledModelPickerProps extends GroupedModelPickerProps {
	label: string;
	/** Shown under the picker, for example why a button is still disabled. */
	hint?: string;
}

/** The shared GroupedModelPicker under a visible label, and an optional hint line under it. */
export function LabeledModelPicker({ label, hint, testId, ...rest }: LabeledModelPickerProps) {
	return (
		<div className="flex flex-col gap-1 text-sm" data-testid={testId ? `${testId}-field` : undefined}>
			{/* The visible label; the box itself carries the same text as its accessible name. */}
			<span className="text-muted-foreground" aria-hidden="true">
				{label}
			</span>
			<GroupedModelPicker testId={testId} label={label} {...rest} />
			{hint && (
				<p className="text-muted-foreground text-xs" data-testid={testId ? `${testId}-hint` : undefined}>
					{hint}
				</p>
			)}
		</div>
	);
}
