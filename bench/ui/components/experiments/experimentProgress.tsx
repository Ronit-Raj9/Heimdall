import { humanExperimentName } from "@/components/home/home.utils";
import { EventTicker, ExperimentStatePill, LiveStat, SlotBar, SlotRow, Spend } from "@/components/live/liveParts";
import { formatElapsed } from "@/components/live/liveExperiment.reducer";
import { ExperimentRunActions } from "@/components/platform/experimentActions";
import { formatValueNumber } from "@/components/valueCell.utils";
import { Button } from "@/components/ui/button";
import { useLiveExperiment } from "@/lib/hooks/useLiveExperiment";
import { getLocale } from "@/lib/i18n";
import type { ExperimentStatus } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { TERMINAL_STATES } from "./experiments.utils";
import { ArrowRight, ChevronDown, CircleAlert, Clock, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

export interface ExperimentProgressCardProps {
	status: ExperimentStatus;
	/** The spend cap the experiment was created with, when this page knows it. */
	capUsd?: number;
	onNew?: () => void;
}

/**
 * Live progress of one experiment: overall bar, done / failed / spend, one row per active slot
 * with its stage and elapsed time, and a ticker of the latest events. Follows the experiment's
 * event stream itself; `status` seeds it and keeps it current when the page polls too.
 */
export function ExperimentProgressCard({ status: given, capUsd, onNew }: ExperimentProgressCardProps) {
	const { t } = useTranslation("live");
	const live = useLiveExperiment(given.exp_id, { initial: given });
	// A terminal status from the page wins; otherwise the live poll is the newest.
	const status = TERMINAL_STATES.has(given.state) ? given : (live.status ?? given);
	const { total, done, running, failed } = status.slots;
	const finishedCount = done + failed;
	const pct = total > 0 ? Math.round((finishedCount / total) * 100) : 0;
	const detail = t("progress.slotsDetail", { done, running, failed, total });

	return (
		<section
			className="bg-card flex min-w-0 flex-col gap-5 rounded-xl border p-5 shadow-sm sm:p-6"
			data-testid="experiment-progress"
			data-state={status.state}
			aria-label={status.name || status.exp_id}
		>
			<header className="flex min-w-0 flex-wrap items-start justify-between gap-3">
				<div className="flex min-w-0 flex-col gap-1">
					<h2 className="truncate text-lg font-semibold tracking-tight" title={status.name || status.exp_id}>
						{humanExperimentName(status, { check: t("progress.checkName"), untitled: t("progress.untitled") })}
					</h2>
					<span className="text-muted-foreground/70 truncate font-mono text-[11px]" title={status.exp_id}>
						{status.exp_id}
					</span>
				</div>
				<div className="flex items-center gap-2">
					{live.elapsedMs !== undefined && (
						<span className="text-muted-foreground flex items-center gap-1 text-xs tabular-nums" title={t("progress.elapsed")}>
							<Clock className="size-3.5" aria-hidden="true" />
							{formatElapsed(live.elapsedMs)}
						</span>
					)}
					<ExperimentStatePill state={status.state} testId="experiment-progress-state" />
				</div>
			</header>

			<div className="flex flex-col gap-2">
				<div className="flex items-baseline justify-between gap-3 text-sm">
					<span className="text-muted-foreground tabular-nums" data-testid="experiment-progress-slots" title={detail}>
						{t("progress.slots", { finished: finishedCount, total })}
					</span>
					<span className="font-semibold tabular-nums">{pct}%</span>
				</div>
				<SlotBar slots={status.slots} label={detail} className="h-2.5" />
			</div>

			<div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
				<LiveStat label={t("progress.done")} tone="good" testId="experiment-progress-done">
					{done}
				</LiveStat>
				<LiveStat label={t("progress.runningCount")} tone={running > 0 ? "live" : undefined} testId="experiment-progress-running">
					{running}
				</LiveStat>
				<LiveStat label={t("progress.failed")} tone={failed > 0 ? "bad" : undefined} testId="experiment-progress-failed">
					{failed}
				</LiveStat>
				<LiveStat label={t("progress.spent")}>
					<span className="flex flex-wrap items-baseline gap-1">
						<Spend status={status} testId="experiment-progress-spent" />
						{capUsd !== undefined && (
							<span className="text-muted-foreground text-xs font-normal">
								{t("progress.cap", { cap: formatValueNumber(capUsd, "usd", getLocale()) })}
							</span>
						)}
					</span>
				</LiveStat>
			</div>

			{status.problem && (
				<div
					className="border-chart-warning/40 bg-chart-warning/10 text-chart-warning-ink flex flex-col gap-2 rounded-lg border p-3 text-sm"
					role="status"
					data-testid="experiment-progress-problem"
				>
					<p className="flex items-start gap-1.5">
						<CircleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
						<span className="min-w-0 break-words">{t("progress.problem", { problem: status.problem })}</span>
					</p>
					{status.run_job_id && (
						<Link
							to="/jobs/$jobId"
							params={{ jobId: status.run_job_id }}
							className="text-primary w-fit text-xs font-medium underline-offset-2 hover:underline"
							data-testid="experiment-progress-problem-log"
						>
							{t("progress.problemJobLog")}
						</Link>
					)}
				</div>
			)}

			{(live.slots.length > 0 || (live.live && !status.problem)) && (
				<div className="flex flex-col gap-2">
					<h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{t("progress.slotsTitle")}</h3>
					{live.slots.length > 0 ? (
						<ul className="-mx-1 flex flex-col gap-1" data-testid="experiment-progress-slot-rows">
							{live.slots.map((slot) => (
								<SlotRow key={slot.slotId} slot={slot} />
							))}
						</ul>
					) : (
						<p className="text-muted-foreground text-sm">{t("progress.noSlots")}</p>
					)}
				</div>
			)}

			{(live.events.length > 0 || live.live) && (
				<div className="flex flex-col gap-2 border-t pt-4">
					<h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{t("progress.activity")}</h3>
					<EventTicker events={live.events} limit={5} testId="experiment-progress-ticker" />
				</div>
			)}

			{live.error !== undefined && (
				<p className="text-chart-warning-ink flex items-center gap-1.5 text-xs" role="status">
					<CircleAlert className="size-3.5" aria-hidden="true" />
					{t("progress.loadError")}
				</p>
			)}

			<div className="flex flex-wrap items-center gap-2">
				{status.state === "done" && (
					<Button asChild size="sm" dataTestId="experiment-open-verdict">
						<Link to="/experiments/$expId" params={{ expId: status.exp_id }}>
							{t("progress.openVerdict")}
							<ArrowRight className="size-3.5" aria-hidden="true" />
						</Link>
					</Button>
				)}
				{/* Jobs: cancel a running experiment, resume a cancelled or failed one. */}
				<ExperimentRunActions status={status} />
				{onNew && (
					<Button variant="ghost" size="sm" onClick={onNew} dataTestId="experiment-plan-another">
						<Plus className="size-3.5" aria-hidden="true" />
						{t("progress.newExperiment")}
					</Button>
				)}
			</div>

			<details className="group text-xs">
				<summary className="text-muted-foreground hover:text-foreground flex cursor-pointer list-none items-center gap-1 select-none">
					<ChevronDown className="size-3.5 transition-transform group-open:rotate-180" aria-hidden="true" />
					{t("progress.details")}
				</summary>
				<dl className="text-muted-foreground mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
					<dt>{t("progress.manifest")}</dt>
					<dd className="truncate font-mono text-[11px]" title={status.manifest_hash}>
						{status.manifest_hash}
					</dd>
				</dl>
			</details>
		</section>
	);
}