import { ImporterList } from "@/components/imports/importerCard";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { AddYourOwnPanel } from "@/components/suites/addYourOwnPanel";
import { useGetImportsQuery } from "@/lib/store";
import { AlertTriangle, ShieldAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The Import tab of Benchmarks (/suites?tab=import; the old /imports and /suites/add). Import a public benchmark with the exact commands
 * of each importer the server links, or bring your own tasks and suite.
 */
export default function AddSuiteView() {
	const { t } = useTranslation("bench");
	const query = useGetImportsQuery();
	const data = query.data;
	return (
		<section className="flex min-w-0 flex-col gap-8" data-testid="imports-screen">
			<PageHeader
				title={t("suites.addFlow.title")}
				subtitle={t("suites.addFlow.subtitle")}
				meta={<ModeBadge mode={data?.mode} isLoading={query.isLoading} isError={query.isError && !data} />}
			/>
			<Section title={t("imports.title")} description={t("imports.subtitle")} testId="imports-section">
				{data?.mode === "synthetic" && (
					<p
						role="note"
						className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-sm border px-4 py-2 text-sm"
						data-testid="imports-synthetic-banner"
					>
						<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
						{t("imports.synthetic")}
					</p>
				)}
				<p
					role="note"
					className="border-chart-info/50 bg-chart-info/10 text-foreground flex items-start gap-2 rounded-sm border px-3 py-2 text-xs"
					data-testid="imports-untrusted-note"
				>
					<ShieldAlert className="text-chart-info-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
					{t("imports.untrusted")}
				</p>
				{query.isLoading ? (
					<BlockSkeleton rows={6} testId="imports-loading" />
				) : query.isError || !data ? (
					<LoadError title={t("imports.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="imports-error" />
				) : (
					<ImporterList importers={data.importers} fetching={query.isFetching} />
				)}
			</Section>
			<AddYourOwnPanel />
		</section>
	);
}