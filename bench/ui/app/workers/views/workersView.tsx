import { ModeBadge } from "@/components/modeBadge";
import { PageHeader } from "@/components/shell";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { WorkersOff, WorkersTable } from "@/components/workers/workersTable";
import { isWorkersOff, useGetWorkersQuery } from "@/lib/store/apis/benchApi";
import { cn } from "@/lib/utils";
import { AlertTriangle, Server } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The workers screen: remote workers with their state and when each was last seen. When worker
 * mode is off (enabled false, or a 404 from a server without the route) the screen says so
 * instead of showing an error or an empty table.
 */
/** Where runner tokens live: a pointer to Settings, runners tab (create a token, copy its start command). */
export function AddRunnerCard() {
	const { t } = useTranslation("operate");
	return (
		<div className="bg-card flex min-w-0 flex-wrap items-center justify-between gap-3 rounded-sm border px-4 py-3" data-testid="workers-add-runner">
			<div className="flex min-w-0 items-start gap-2">
				<Server className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden="true" />
				<div className="min-w-0">
					<p className="text-foreground text-sm font-medium">{t("workers.runnersTitle")}</p>
					<p className="text-muted-foreground text-xs">{t("workers.runnersBody")}</p>
				</div>
			</div>
			<a href="/settings?tab=runners" className="text-primary text-sm font-medium underline-offset-4 hover:underline" data-testid="workers-runner-tokens-link">
				{t("workers.runnersLink")}
			</a>
		</div>
	);
}

export default function WorkersView() {
	const { t } = useTranslation("bench");
	const query = useGetWorkersQuery();
	const data = query.data;
	const off = !query.isLoading && isWorkersOff(query.isError ? query.error : undefined, data);
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="workers-screen">
			<PageHeader
				title={t("workers.title")}
				subtitle={t("workers.subtitle")}
				meta={(data || !off) && <ModeBadge mode={data?.mode} isLoading={query.isLoading} isError={query.isError && !data} />}
			/>
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
			<AddRunnerCard />
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