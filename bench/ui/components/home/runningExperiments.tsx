import { formatRelative, formatWhen } from "@/components/platform/platform.utils";
import { EntityLink, StatusBadge } from "@/components/shell";
import { ValueCell } from "@/components/valueCell";
import { getLocale } from "@/lib/i18n";
import type { ExperimentStatus, LiveEvent } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CircleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { slotShares } from "./home.utils";
import { useExperimentEvents } from "./useExperimentEvents";

const KNOWN_EVENTS = new Set(["slot_started", "stage", "slot_sealed", "budget_stop", "experiment_done", "error", "end"]);

/** Live progress of each running experiment, one card per experiment. */
export function RunningExperiments({ experiments, onFinished }: { experiments: readonly ExperimentStatus[]; onFinished?: () => void }) {
	const { t } = useTranslation("bench");
	if (experiments.length === 0) {
		return (
			<p className="text-muted-foreground rounded-lg border border-dashed px-4 py-3 text-sm" data-testid="home-running-empty">
				{t("home.running.empty")}
			</p>
		);
	}
	return (
		<ul className="grid gap-3 lg:grid-cols-2" data-testid="home-running-list">
			{experiments.map((exp) => (
				<li key={exp.exp_id} className="min-w-0">
					<RunningCard initial={exp} onFinished={onFinished} />
				</li>
			))}
		</ul>
	);
}

/** One running experiment: state, the slot bar, spend, and the newest event, polled live. */
export function RunningCard({ initial, onFinished }: { initial: ExperimentStatus; onFinished?: () => void }) {
	const { status, events, error } = useExperimentEvents(initial, onFinished);
	return <RunningCardBody status={status} events={events} error={error} />;
}

export function RunningCardBody({ status, events, error }: { status: ExperimentStatus; events: readonly LiveEvent[]; error?: unknown }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const { total, done, running, failed } = status.slots;
	const shares = slotShares(status.slots);
	const last = events.length > 0 ? events[events.length - 1] : undefined;

	return (
		<article
			className="bg-card flex h-full min-w-0 flex-col gap-3 rounded-lg border p-4 shadow-(--shadow-card)"
			data-testid={`home-running-${status.exp_id}`}
			data-state={status.state}
			aria-label={status.name || status.exp_id}
		>
			<div className="flex min-w-0 items-start gap-2">
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<EntityLink kind="experiment" id={status.exp_id} className="truncate text-sm font-medium no-underline">
						{status.name || status.exp_id}
					</EntityLink>
					{status.name && (
						<span className="text-muted-foreground truncate font-mono text-[11px]" title={status.exp_id}>
							{status.exp_id}
						</span>
					)}
				</div>
				<StatusBadge state={status.state} testId={`home-running-state-${status.exp_id}`} />
			</div>
			<div className="flex flex-col gap-1.5">
				<div
					className="bg-chart-track flex h-2 w-full overflow-hidden rounded-full"
					role="progressbar"
					aria-valuemin={0}
					aria-valuemax={total}
					aria-valuenow={done + failed}
					aria-label={t("home.running.slotsDetail", { done, running, failed, total })}
				>
					<div className="bg-chart-success h-full transition-[width] duration-500" style={{ width: shares.done }} />
					<div className="bg-chart-error h-full transition-[width] duration-500" style={{ width: shares.failed }} />
					<div
						className="bg-chart-info h-full animate-pulse transition-[width] duration-500 motion-reduce:animate-none"
						style={{ width: shares.running }}
					/>
				</div>
				<div className="text-muted-foreground flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 text-xs tabular-nums">
					<span data-testid={`home-running-slots-${status.exp_id}`} title={t("home.running.slotsDetail", { done, running, failed, total })}>
						{t("home.running.slots", { done: done + failed, total })}
					</span>
					<span className="flex items-baseline gap-1.5">
						{t("home.running.spent")}
						<ValueCell value={status.spent_usd} kind="usd" className="text-foreground" />
					</span>
				</div>
			</div>
			<div
				className="border-border/70 mt-auto flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 border-t pt-2.5 text-xs"
				data-testid={`home-running-last-${status.exp_id}`}
			>
				{error && !last ? (
					<span className="text-chart-error-ink flex items-center gap-1.5">
						<CircleAlert className="size-3.5" aria-hidden="true" />
						{t("home.running.loadError")}
					</span>
				) : last ? (
					<LastEvent event={last} locale={locale} />
				) : (
					<span className="text-muted-foreground">{t("home.running.noEvents")}</span>
				)}
			</div>
		</article>
	);
}

function LastEvent({ event, locale }: { event: LiveEvent; locale: string }) {
	const { t } = useTranslation("bench");
	const kind = KNOWN_EVENTS.has(event.kind) ? event.kind : null;
	const label = kind
		? t(`home.running.event.${kind as "slot_started" | "stage" | "slot_sealed" | "budget_stop" | "experiment_done" | "error" | "end"}`, {
				stage: event.stage ?? "",
			})
		: event.kind;
	return (
		<>
			<span className="text-muted-foreground shrink-0">{t("home.running.last")}</span>
			<span className={cn("min-w-0", event.kind === "error" ? "text-chart-error-ink" : "text-foreground")}>
				{label}
				{event.message ? `: ${event.message}` : ""}
			</span>
			{event.run_id && <EntityLink kind="run" id={event.run_id} short={14} className="shrink-0 text-[11px]" />}
			{event.outcome && <span className="text-muted-foreground shrink-0">{event.outcome}</span>}
			<time
				className="text-muted-foreground ml-auto shrink-0 tabular-nums"
				dateTime={event.at}
				title={formatWhen(event.at, locale) ?? undefined}
			>
				{formatRelative(event.at, locale)}
			</time>
		</>
	);
}