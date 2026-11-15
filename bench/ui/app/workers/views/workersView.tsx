import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { WorkersOff, WorkersTable } from "@/components/workers/workersTable";
import { isWorkersOff, useGetWorkersQuery } from "@/lib/store/apis/benchApi";
import { cn } from "@/lib/utils";
import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The workers screen: remote workers with their state and when each was last seen. When worker
 * mode is off (enabled false, or a 404 from a server without the route) the screen says so
 * instead of showing an error or an empty table.
 */
export default function WorkersView() {
	const { t } = useTranslation("bench");
	const query = useGetWorkersQuery();
	const data = query.data;
	const off = !query.isLoading && isWorkersOff(query.isError ? query.error : undefined, data);
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="workers-screen">
			<header className="flex flex-col gap-1">
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("workers.title")}</h1>
					{(data || !off) && <ModeBadge mode={data?.mode} isLoading={query.isLoading} isError={query.isError && !data} />}
				</div>
				<p className="text-muted-foreground max-w-3xl text-sm">{t("workers.subtitle")}</p>
			</header>
			{data?.mode === "synthetic" && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-sm border px-4 py-2 text-sm"
					data-testid="workers-synthetic-banner"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("workers.synthetic")}
				</p>
			)}
			{query.isLoading ? (
				<BlockSkeleton rows={4} testId="workers-loading" />
			) : off ? (
				<WorkersOff />
			) : query.isError || !data ? (
				<LoadError title={t("workers.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="workers-error" />
			) : data.workers.length === 0 ? (
				<EmptyNote testId="workers-empty">{t("workers.empty")}</EmptyNote>
			) : (
				<div className={cn("min-w-0 transition-opacity", query.isFetching && "opacity-60")} aria-busy={query.isFetching}>
					<WorkersTable workers={data.workers} />
				</div>
			)}
		</section>
	);
}