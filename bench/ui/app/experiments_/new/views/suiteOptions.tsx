import "./builderI18n";

import { isPracticeExperiment } from "@/components/experiments/experiments.utils";
import { SelectGroup, SelectItem, SelectLabel } from "@/components/ui/select";
import type { Catalog, CatalogSuite } from "@/lib/types/bench";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { suiteRef } from "./experimentFormSchema";
import { suiteMenuGroups, type ReadyTasks } from "./matrixBuilderSchema";

/**
 * A benchmark as the menus name it (N39): its ready count when known ("3 of 5 ready"), else its task
 * count, and "(practice)" after a practice benchmark.
 */
export function suiteOptionText(suite: CatalogSuite, ready: ReadyTasks | undefined, t: TFunction<"builder">): string {
	const entry = ready?.get(suiteRef(suite));
	const option = entry
		? t("fields.suiteOptionReady", { id: suite.id, version: suite.version, ready: entry.ids.length, total: entry.total })
		: t("fields.suiteOption", { id: suite.id, version: suite.version, count: (suite.tasks ?? []).length });
	return isPracticeExperiment({ suite: suite.id }) ? t("fields.practiceOption", { option }) : option;
}

/**
 * The benchmark menu items of both new comparison forms: benchmarks with ready tasks first, then a
 * "Not ready yet" group of benchmarks with none, disabled and saying what to do.
 */
export function SuiteMenuItems({
	catalog,
	ready,
	testIdPrefix,
}: {
	catalog: Catalog;
	ready: ReadyTasks | undefined;
	testIdPrefix: string;
}) {
	const { t } = useTranslation("builder");
	const groups = suiteMenuGroups(catalog, ready);
	return (
		<>
			{groups.ready.map((s) => (
				<SelectItem key={suiteRef(s)} value={suiteRef(s)} data-testid={`${testIdPrefix}-${s.id}`}>
					{suiteOptionText(s, ready, t)}
				</SelectItem>
			))}
			{groups.notReady.length > 0 && (
				<SelectGroup data-testid={`${testIdPrefix}-not-ready`}>
					<SelectLabel>{t("fields.notReadyGroup")}</SelectLabel>
					{groups.notReady.map((s) => (
						<SelectItem
							key={suiteRef(s)}
							value={suiteRef(s)}
							disabled
							title={t("fields.notReadyTitle")}
							data-testid={`${testIdPrefix}-${s.id}`}
						>
							{suiteOptionText(s, ready, t)}
						</SelectItem>
					))}
				</SelectGroup>
			)}
		</>
	);
}