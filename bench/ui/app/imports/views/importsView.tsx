import { ImporterList } from "@/components/imports/importerCard";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { useGetImportsQuery } from "@/lib/store";
import { AlertTriangle, ShieldAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

/** The imports screen: every importer the server links, with what it needs and the exact commands. */
export default function ImportsView() {
	const { t } = useTranslation("bench");
	const query = useGetImportsQuery();
	const data = query.data;
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="imports-screen">
			<header className="flex flex-col gap-1">
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("imports.title")}</h1>
					<ModeBadge mode={data?.mode} isLoading={query.isLoading} isError={query.isError && !data} />
				</div>
				<p className="text-muted-foreground max-w-3xl text-sm">{t("imports.subtitle")}</p>
			</header>
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
		</section>
	);
}