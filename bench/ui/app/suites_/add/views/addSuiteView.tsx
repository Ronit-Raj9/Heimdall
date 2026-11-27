import { ImporterList } from "@/components/imports/importerCard";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { AddChooser } from "@/components/suites/addChooserView";
import { AddYourOwnPanel } from "@/components/suites/addYourOwnPanel";
import { useGetImportsQuery } from "@/lib/store";
import { Link } from "@tanstack/react-router";
import { w4m5Text } from "@/components/onboard/w4m5I18n";
import { AlertTriangle, FileCode2, FilePlus2, ShieldAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The Import tab of Benchmarks (/suites?tab=import; the old /imports). Import a public benchmark with the exact commands
 * of each importer the server links, or bring your own tasks and suite.
 */
export default function AddSuiteView() {
	const { t } = useTranslation("bench");
	const { t: ta } = useTranslation("appTasks");
	const query = useGetImportsQuery();
	const data = query.data;
	return (
		<section className="flex min-w-0 flex-col gap-8" data-testid="imports-screen">
			{/* X-42: only shown as the Import tab of the Benchmarks hub, whose header holds the h1. */}
			<div className="flex flex-col gap-1">
				<div className="flex flex-wrap items-center gap-2">
					<h2 className="text-lg font-semibold">{t("suites.addFlow.title")}</h2>
					<ModeBadge mode={data?.mode} isLoading={query.isLoading} isError={query.isError && !data} />
				</div>
			</div>
			<AddChooser />
			<Section title={t("imports.title")} description={t("imports.subtitle")} testId="imports-section">
				{data?.mode === "synthetic" && (
					<p
						role="note"
						className="border-warning bg-warning-soft text-foreground flex items-start gap-2 rounded-sm border px-4 py-2 text-sm"
						data-testid="imports-synthetic-banner"
					>
						<AlertTriangle className="text-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
						{t("imports.synthetic")}
					</p>
				)}
				<p
					role="note"
					className="border-info bg-info-soft text-foreground flex items-start gap-2 rounded-sm border px-3 py-2 text-xs"
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
			<Link
				to="/suites/add"
				className="hover:bg-muted flex w-fit items-center gap-2 rounded-md border px-3 py-2 text-sm"
				data-testid="imports-add-file"
			>
				<FilePlus2 className="size-4" aria-hidden="true" />
				{w4m5Text("imports.addFile")}
			</Link>
			<Link
				to="/suites/from-api"
				className="hover:bg-muted flex w-fit items-center gap-2 rounded-md border px-3 py-2 text-sm"
				data-testid="imports-from-api"
			>
				<FileCode2 className="size-4" aria-hidden="true" />
				{ta("entry")}
			</Link>
			<AddYourOwnPanel />
		</section>
	);
}