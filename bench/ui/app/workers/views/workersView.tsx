import { ModeBadge } from "@/components/modeBadge";
import { ConnectedWorkers } from "@/components/workers/connectedWorkers";
import { PageHeader } from "@/components/shell";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { isSharedWorker, SharedWorkerNote, WorkerStateBadge, WorkersOff } from "@/components/workers/workersTable";
import { formatRelative } from "@/components/platform/platform.utils";
import { getLocale } from "@/lib/i18n";
import type { WorkerInfo } from "@/lib/types/bench";
import { StatusDot, type Tone } from "@/app/operate/views/uxBits";
import { isWorkersOff, useGetWorkersQuery } from "@/lib/store/apis/benchApi";
import { cn } from "@/lib/utils";
import { AlertTriangle, HardDrive, Server } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The workers screen: remote workers with their state and when each was last seen. When worker
 * mode is off (enabled false, or a 404 from a server without the route) the screen says so
 * instead of showing an error or an empty table.
 */
/** Where worker tokens live: a pointer to Settings, workers tab (create a token, copy its start command). */
export function AddRunnerCard() {
	const { t } = useTranslation("operate");
	return (
		<div className="bg-card flex min-w-0 flex-wrap items-center justify-between gap-3 rounded-xl border px-5 py-4" data-testid="workers-add-runner">
			<div className="flex min-w-0 items-start gap-3">
				<span className="bg-muted flex size-9 shrink-0 items-center justify-center rounded-lg">
					<Server className="size-4" aria-hidden="true" />
				</span>
				<div className="min-w-0">
					<p className="text-foreground text-sm font-medium">{t("workers.runnersTitle")}</p>
					<p className="text-muted-foreground text-xs">{t("workers.runnersBody")}</p>
				</div>
			</div>
			<a
				href="/settings?tab=runners"
				className="bg-primary text-primary-foreground hover:bg-primary/90 rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
				data-testid="workers-runner-tokens-link"
			>
				{t("workers.runnersLink")}
			</a>
		</div>
	);
}

const STATE_TONE: Record<string, Tone> = { running: "good", idle: "idle", lost: "bad" };

/** One worker: online pulse, a human title (raw id on hover only), host, slots in use as a bar, completed runs and last heartbeat. */
export function RunnerCard({ worker }: { worker: WorkerInfo }) {
	const { t } = useTranslation("operate");
	const running = (worker.running ?? []).length;
	const pct = worker.slots > 0 ? Math.min(100, (running / worker.slots) * 100) : 0;
	const tone = STATE_TONE[worker.state] ?? "idle";
	const seen = formatRelative(worker.last_seen, getLocale());
	const shared = isSharedWorker(worker.id);
	const title = shared ? t("workers.sharedTitle") : worker.id || worker.host;
	return (
		<article
			className="bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5 transition-shadow hover:shadow-md"
			data-testid={`workers-row-${worker.id}`}
			data-state={worker.state}
		>
			<header className="flex items-start justify-between gap-3">
				<div className="flex min-w-0 items-center gap-3">
					<span className="bg-muted relative flex size-9 shrink-0 items-center justify-center rounded-lg">
						<HardDrive className="size-4" aria-hidden="true" />
						<StatusDot tone={tone} pulse={worker.state === "running"} className="absolute -top-0.5 -right-0.5" />
					</span>
					<div className="min-w-0">
						<p className="text-foreground truncate text-base font-semibold" title={worker.id}>
							{title}
						</p>
						{worker.host && worker.host !== title && (
							<p className="text-muted-foreground truncate text-xs" title={worker.host}>
								{worker.host}
							</p>
						)}
					</div>
				</div>
				<WorkerStateBadge state={worker.state} testId={`workers-row-${worker.id}-state`} />
			</header>
			{shared && <SharedWorkerNote slots={worker.slots} testId={`workers-row-${worker.id}-shared-note`} />}
			<div className="flex flex-col gap-1.5">
				<div className="flex items-baseline justify-between text-xs">
					<span className="text-muted-foreground">{t("workers.cards.slots")}</span>
					<span className="text-foreground tabular-nums" title={(worker.running ?? []).join(", ") || undefined}>
						{running} / {worker.slots}
					</span>
				</div>
				<div className="bg-muted h-1.5 overflow-hidden rounded-full">
					<div
						className={running > 0 ? "bg-primary h-full rounded-full transition-all" : "h-full"}
						style={{ width: `${pct}%` }}
					/>
				</div>
			</div>
			<dl className="grid grid-cols-2 gap-3 text-xs">
				<div className="min-w-0">
					<dt className="text-muted-foreground">{t("workers.cards.done")}</dt>
					<dd className="text-foreground text-sm font-medium tabular-nums">{worker.completed}</dd>
				</div>
				<div className="min-w-0">
					<dt className="text-muted-foreground">{t("workers.cards.seen")}</dt>
					<dd className="text-foreground truncate text-sm font-medium">
						<time dateTime={worker.last_seen} title={worker.last_seen}>
							{seen ?? "-"}
						</time>
					</dd>
				</div>
			</dl>
			<p className="text-muted-foreground truncate font-mono text-[11px]" title={worker.version}>
				{t("workers.cards.build", { version: worker.version })}
			</p>
		</article>
	);
}

export default function WorkersView() {
	const { t } = useTranslation("bench");
	const query = useGetWorkersQuery();
	const data = query.data;
	const off = !query.isLoading && isWorkersOff(query.isError ? query.error : undefined, data);
	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-6" data-testid="workers-screen">
			<PageHeader
				title={t("workers.title")}
				subtitle={t("workers.subtitle")}
				meta={(data || !off) && <ModeBadge mode={data?.mode} isLoading={query.isLoading} isError={query.isError && !data} />}
			/>
			{data?.mode === "synthetic" && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-lg border px-4 py-2 text-sm"
					data-testid="workers-synthetic-banner"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("workers.synthetic")}
				</p>
			)}
			<ConnectedWorkers />
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
					<ul className="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3" aria-label={t("workers.label")} data-testid="workers-table">
						{data.workers.map((w) => (
							<li key={w.id} className="min-w-0">
								<RunnerCard worker={w} />
							</li>
						))}
					</ul>
				</div>
			)}
		</section>
	);
}