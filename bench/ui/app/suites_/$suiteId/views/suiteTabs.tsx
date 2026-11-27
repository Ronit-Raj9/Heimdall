import { SuiteExportMenu } from "@/components/export/exportMenu";
import { Section } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell";
import { CopyCodeBlock } from "@/components/suites/copyCodeBlock";
import { CapabilityCoverage, LicenseSummary, ProbeList } from "@/components/suites/suiteCoverage";
import { SuiteHealthSummary } from "@/components/suites/suiteHealth";
import { isRedacted, type HealthState } from "@/components/suites/suiteHealth.utils";
import { ImportedFrom } from "@/components/suites/suiteScenario";
import { SuiteTiers } from "@/components/suites/suiteTiers";
import type { SuiteSummary } from "@/lib/types/bench";
import { Download } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

/** Health tab: quarantine, held-out and canary state, then capability coverage, licenses and probes. */
export function SuiteHealthTab({ suite, health }: { suite: SuiteSummary; health: HealthState }) {
	// Withheld held-out tasks carry no capabilities or license, so coverage and licenses leave them out.
	const publicTasks = useMemo(() => suite.tasks.filter((task) => !isRedacted(task)), [suite.tasks]);
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-health-tab">
			<SuiteHealthSummary suite={suite} health={health} />
			<div className="grid min-w-0 grid-cols-1 gap-6 lg:grid-cols-2">
				<CapabilityCoverage tasks={publicTasks} />
				<LicenseSummary tasks={publicTasks} />
			</div>
			<ProbeList probes={suite.probes} />
		</div>
	);
}

/** Tiers tab: each tier's tasks, and how to pick a tier or have one suggested. */
export function SuiteTiersTab({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suite-tiers-tab">
			<SuiteTiers suite={suite} />
			<Section title={t("suites.tiers.useTitle")} description={t("suites.tiers.useDescription")} testId="suite-tiers-use">
				<div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
					<CopyCodeBlock
						code={`# experiment.yaml\nsuite: ${suite.id}\ntier: pr\nconditions:\n  - {id: clean, tasks: all, trials: 2}`}
						label={t("suites.tiers.useTitle")}
						testId="suite-tiers-experiment"
					/>
					<CopyCodeBlock code={`heimdall suite tiers suggest ${suite.id}`} label={t("suites.tiers.suggest")} testId="suite-tiers-suggest" />
				</div>
			</Section>
		</div>
	);
}

/** Import and export tab: where an imported suite came from, and the suite as Harbor task folders. */
export function SuiteExportTab({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	return (
		<div className="grid min-w-0 grid-cols-1 items-start gap-6 lg:grid-cols-2" data-testid="suite-export-tab">
			<Section title={t("suites.importExport.importTitle")} testId="suite-import-section">
				{suite.imported ? (
					<ImportedFrom record={suite.imported} />
				) : (
					<EmptyState
						compact
						icon={Download}
						message={t("suites.importExport.notImported")}
						action={{ label: t("suites.addSuite"), to: "/suites/add" }}
						testId="suite-not-imported"
					/>
				)}
			</Section>
			<Section
				title={t("suites.importExport.exportTitle")}
				description={t("suites.importExport.exportDescription")}
				testId="suite-export-section"
			>
				{/* W10 export: the suite as Harbor task folders, verifiers withheld */}
				<SuiteExportMenu suiteId={suite.id} inline />
			</Section>
		</div>
	);
}