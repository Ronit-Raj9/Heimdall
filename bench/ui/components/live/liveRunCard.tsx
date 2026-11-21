// A compact live card for one experiment, for Home and other summary pages: name, state pill,
// slot bar, done / total, spend, elapsed, the active slots and the newest event line.
import { useLiveExperiment } from "@/lib/hooks/useLiveExperiment";
import type { ExperimentStatus } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { CircleAlert, Clock } from "lucide-react";
import { useTranslation } from "react-i18next";
import { formatElapsed } from "./liveExperiment.reducer";
import { ExperimentStatePill, LiveDot, SlotBar, SlotRow, Spend, useEventLabel } from "./liveParts";

export interface LiveRunCardProps {
	/** The status the page already has, for example a row of GET /experiments. */
	experiment: ExperimentStatus;
	/** Active slot rows shown under the bar; 0 hides them. */
	maxSlots?: number;
	/** Called once when the experiment reaches a terminal state. */
	onFinished?: () => void;
	className?: string;
}

/** One experiment, followed live (2 s polling) while it runs. */
export function LiveRunCard({ experiment, maxSlots = 2, onFinished, className }: LiveRunCardProps) {
	const { t } = useTranslation("live");
	const label = useEventLabel();
	const live = useLiveExperiment(experiment.exp_id, { initial: experiment, onFinished, finishedKeep: 0 });
	const status = live.status ?? experiment;
	const { total, done, running, failed } = status.slots;
	const detail = t("progress.slotsDetail", { done, running, failed, total });
	const slots = live.slots.slice(0, maxSlots);

	return (
		<article
			className={cn(
				"group bg-card relative flex h-full min-w-0 flex-col gap-3 rounded-xl border p-5 transition-shadow hover:shadow-md",
				className,
			)}
			data-testid={`live-run-card-${status.exp_id}`}
			data-state={status.state}
			aria-label={status.name || status.exp_id}
		>
			<div className="flex min-w-0 items-start justify-between gap-2">
				<Link
					to="/experiments/$expId"
					params={{ expId: status.exp_id }}
					className="min-w-0 truncate text-base font-semibold tracking-tight after:absolute after:inset-0 after:content-['']"
					title={status.name || status.exp_id}
				>
					{status.name || status.exp_id}
				</Link>
				<ExperimentStatePill state={status.state} />
			</div>

			<div className="flex flex-col gap-1.5">
				<SlotBar slots={status.slots} label={detail} />
				<div className="text-muted-foreground flex items-baseline justify-between gap-3 text-xs tabular-nums">
					<span title={detail}>{t("progress.slots", { finished: done + failed, total })}</span>
					<span className="flex items-baseline gap-3">
						{live.elapsedMs !== undefined && (
							<span className="flex items-center gap-1">
								<Clock className="size-3 self-center" aria-hidden="true" />
								{formatElapsed(live.elapsedMs)}
							</span>
						)}
						<span className="text-foreground">
							<Spend status={status} />
						</span>
					</span>
				</div>
			</div>

			{slots.length > 0 && (
				<ul className="-mx-2 flex flex-col gap-0.5">
					{slots.map((slot) => (
						<SlotRow key={slot.slotId} slot={slot} compact />
					))}
				</ul>
			)}

			<div className="border-border/70 mt-auto flex min-w-0 items-center gap-2 border-t pt-2.5 text-xs" data-testid={`live-run-card-last-${status.exp_id}`}>
				{live.latest ? (
					<>
						<LiveDot tone={live.live ? "live" : "muted"} />
						<span key={live.latest.seq} className="animate-in fade-in min-w-0 flex-1 truncate duration-500" title={label(live.latest)}>
							{label(live.latest)}
						</span>
					</>
				) : live.error !== undefined ? (
					<span className="text-chart-warning-ink flex items-center gap-1.5">
						<CircleAlert className="size-3.5" aria-hidden="true" />
						{t("progress.loadError")}
					</span>
				) : (
					<span className="text-muted-foreground">{t("progress.noEvents")}</span>
				)}
			</div>
		</article>
	);
}
