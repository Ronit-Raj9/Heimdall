import { KeyMissingNote } from "@/components/models/keyMissingNote";
import { type CatalogProvider, groupModels, keyStateFor, providerOfModel, type KeyState } from "@/components/models/groupedModelPicker";
import type { EvaluationArm } from "@/lib/store/apis/evaluationsApi";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import type { ModelKey } from "@/lib/types/models";
import { useTranslation } from "react-i18next";
import "@/lib/types/evaluateI18n";

/**
 * The model key each setup of the wizard uses (Q-11): which provider, its display name, and whether the
 * organization has a working key for it. A setup on the agent's own model (no model) needs no key.
 */
export interface ArmKey {
	label: string;
	provider: string;
	providerName: string;
	state: KeyState;
}

/** Pure part of useArmKeys: one row per setup that has a model; the provider comes from the catalog when the arm has none. */
export function armKeys(
	arms: readonly Pick<EvaluationArm, "label" | "provider" | "model">[],
	catalog: CatalogProvider[],
	keys: ModelKey[],
): ArmKey[] {
	const groups = groupModels(catalog, keys);
	const out: ArmKey[] = [];
	for (const a of arms) {
		if (!a.model) continue;
		const provider = a.provider || providerOfModel(groups, a.model);
		if (!provider) continue;
		out.push({
			label: a.label,
			provider,
			providerName: catalog.find((p) => p.id === provider)?.name ?? provider,
			state: keyStateFor(provider, keys),
		});
	}
	return out;
}

/** The first setup whose provider has no working key, or undefined. */
export function firstMissingKey(rows: readonly ArmKey[]): ArmKey | undefined {
	return rows.find((r) => r.state !== "ok");
}

/** armKeys over GET /models; empty while the list loads or when it failed (the picker says so itself). */
export function useArmKeys(arms: readonly Pick<EvaluationArm, "label" | "provider" | "model">[]): { rows: ArmKey[]; loaded: boolean } {
	const models = useGetModelsQuery();
	if (!models.data) return { rows: [], loaded: false };
	const catalog = (models.data.providers ?? []) as unknown as CatalogProvider[];
	return { rows: armKeys(arms, catalog, models.data.keys ?? []), loaded: true };
}

/**
 * One line per setup: "Uses your OpenAI key", or the shared KeyMissingNote with a way to add one and come
 * back. `okOnly` leaves the missing ones out (step 4, where the model picker already shows them).
 */
export function KeyLines({
	rows,
	returnTo,
	okOnly,
	testId,
}: {
	rows: readonly ArmKey[];
	returnTo: string;
	/** Show only the "Uses your ... key" lines (the picker shows the missing note itself). */
	okOnly?: boolean;
	testId: string;
}) {
	const { t } = useTranslation("evaluate");
	const many = rows.length > 1;
	const shown = okOnly ? rows.filter((r) => r.state === "ok") : rows;
	if (shown.length === 0) return null;
	return (
		<ul className="flex flex-col gap-1 text-xs" data-testid={testId}>
			{shown.map((r) => (
				<li key={r.label} data-testid={`${testId}-${r.label}`} data-key-state={r.state}>
					{r.state === "ok" ? (
						<span className="text-muted-foreground">
							{many ? `${r.label}: ` : ""}
							{t("keyLine.uses", { provider: r.providerName })}
						</span>
					) : (
						<KeyMissingNote
							provider={r.provider}
							providerName={r.providerName}
							returnTo={returnTo}
							className="text-chart-warning-ink flex flex-wrap items-center gap-2 text-xs"
						/>
					)}
				</li>
			))}
		</ul>
	);
}
