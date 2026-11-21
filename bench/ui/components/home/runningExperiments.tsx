import { formatRelative, formatWhen } from "@/components/platform/platform.utils";
import { suiteIdOf } from "@/components/shell";
import { ValueCell } from "@/components/valueCell";
import { getLocale } from "@/lib/i18n";
import type { ExperimentStatus, LiveEvent } from "@/lib/types/bench";
import "@/lib/types/homeI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Activity, ArrowRight, CircleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { LiveDot, TrackBadge } from "./homeBits";
import { slotShares } from "./home.utils";
import { useExperimentEvents } from "./useExperimentEvents";

const KNOWN_EVENTS = new Set(["slot_started", "stage", "slot_sealed", "budget_stop", "experiment_done", "error", "end"]);

/** Live progress of each running experiment, one card per experiment. */
export function RunningExperiments({
	experiments,
	onFinished,
	trackOf,
}: {
	experiments: readonly ExperimentStatus[];
	onFinished?: () => void;
	/** The track of a suite id, for the card's category icon. */
	trackOf?: (suite: string | undefined) => string | undefined;
}) {
	const { t } = useTranslation("home");
	if (experiments.length === 0) {
		return (
			<div
				className="text-muted-foreground flex items-center gap-3 rounded-xl border border-dashed px-5 py-4 text-sm"
				data-testid="home-running-empty"
			>
				<span className="bg-muted flex size-8 items-center justify-center rounded-full">
					<Activity className="size-4" aria-hidden="true" />
				</span>
				{t("running.empty")}
			</div>
		);
	}
	return (
		<ul className="grid gap-4 md:grid-cols-2" data-testid="home-running-list">
			{experiments.map((exp) => (
				<li key={exp.exp_id} className="min-w-0">
					<RunningCard initial={exp} onFinished={onFinished} track={trackOf?.(exp.suite)} />
				</li>
			))}
		</ul>
	);
}

/** One running experiment, polled live every few seconds. */
export function RunningCard({ initial, onFinished, track }: { initial: ExperimentStatus; onFinished?: () => void; track?: string }) {
	const { status, events, error } = useExperimentEvents(initial, onFinished);
	return <RunningCardBody status={status} events={events} error={error} track={track} />;
}

/** The card itself: name, live dot, progress bar with counts and spend, and the newest event. */
export function RunningCardBody({
	status,
	events,
	error,
	track,
}: {
	status: ExperimentStatus;
	events: readonly LiveEvent[];
	error?: unknown;
	track?: string;
}) {
	const { t } = useTranslation("home");
	const { t: tb } = useTranslation("bench");
	const locale = getLocale();
	const { total, done, running, failed } = status.slots;
	const shares = slotShares(status.slots);
	const sealed = done + failed;
	const percent = total > 0 ? Math.round((sealed / total) * 100) : 0;
	const last = events.length > 0 ? events[events.length - 1] : undefined;
	const title = status.name || status.exp_id;
	const live = status.state === "running" || status.state === "analyzing";

	return (
		<article
			className="bg-card group flex h-full min-w-0 flex-col gap-4 rounded-xl border p-5 shadow-(--shadow-card) transition-shadow hover:shadow-md"
			data-testid={`home-running-${status.exp_id}`}
			data-state={status.state}
			aria-label={title}
		>
			<div className="flex min-w-0 items-start gap-3">
				<TrackBadge track={track} />
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<Link
						to="/experiments/$expId"
						params={{ expId: status.exp_id }}
						className="text-foreground focus-visible:ring-ring/60 truncate rounded-sm text-base font-semibold tracking-tight hover:underline focus-visible:ring-2 focus-visible:outline-none"
						title={status.exp_id}
					>
						{title}
					</Link>
					{status.suite && <span className="text-muted-foreground truncate text-xs">{suiteIdOf(status.suite)}</span>}
				</div>
				<span
					className="bg-chart-info/12 text-chart-info-ink inline-flex shrink-0 items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium"
					data-testid={`home-running-state-${status.exp_id}`}
					data-state={status.state}
				>
					{live && <LiveDot className="size-1.5" />}
					{tb(`shell.status.state.${status.state}` as never)}
				</span>
			</div>

			<div className="flex flex-col gap-2">
				<div className="flex items-baseline justify-between gap-3">
					<span className="text-foreground text-2xl font-semibold tracking-tight tabular-nums">{percent}%</span>
					<span className="text-muted-foreground text-xs tabular-nums" data-testid={`home-running-slots-${status.exp_id}`}>
						{t("running.progress", { done: sealed, total })}
					</span>
				</div>
				<div
					className="bg-chart-track flex h-2 w-full overflow-hidden rounded-full"
					role="progressbar"
					aria-valuemin={0}
					aria-valuemax={total}
					aria-valuenow={sealed}
					aria-label={tb("home.running.slotsDetail", { done, running, failed, total })}
				>
					<div className="bg-chart-success h-full transition-[width] duration-700 ease-out" style={{ width: shares.done }} />
					<div className="bg-chart-error h-full transition-[width] duration-700 ease-out" style={{ width: shares.failed }} />
					<div
						className="bg-chart-info h-full animate-pulse transition-[width] duration-700 ease-out motion-reduce:animate-none"
						style={{ width: shares.running }}
					/>
				</div>
			</div>

			<div
				className="bg-muted/50 flex min-h-9 min-w-0 items-center gap-2 rounded-lg px-3 py-2 text-xs"
				data-testid={`home-running-last-${status.exp_id}`}
				aria-live="polite"
			>
				{error && !last ? (
					<span className="text-chart-error-ink flex min-w-0 items-center gap-1.5">
						<CircleAlert className="size-3.5 shrink-0" aria-hidden="true" />
						<span className="truncate">{t("running.loadError")}</span>
					</span>
				) : last ? (
					<LastEvent event={last} locale={locale} />
				) : (
					<span className="text-muted-foreground flex items-center gap-2">
						<LiveDot className="size-1.5" />
						{t("running.waiting")}
					</span>
				)}
			</div>

			<div className="mt-auto flex items-center justify-between gap-3 text-xs">
				<span className="text-muted-foreground flex items-baseline gap-1 tabular-nums">
					<ValueCell value={status.spent_usd} kind="usd" className="text-foreground font-medium" />
					<span>{tb("home.running.spent").toLowerCase()}</span>
				</span>
				<Link
					to="/experiments/$expId"
					params={{ expId: status.exp_id }}
					className="text-primary focus-visible:ring-ring/60 inline-flex items-center gap-1 rounded-sm font-medium focus-visible:ring-2 focus-visible:outline-none"
					data-testid={`home-running-watch-${status.exp_id}`}
				>
					{t("running.watch")}
					<ArrowRight className="size-3.5 transition-transform group-hover:translate-x-0.5 motion-reduce:transition-none" aria-hidden="true" />
				</Link>
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
	const text = `${label}${event.message ? `: ${event.message}` : ""}`;
	return (
		<>
			<span className={cn("size-1.5 shrink-0 rounded-full", event.kind === "error" ? "bg-chart-error" : "bg-chart-info")} aria-hidden="true" />
			<span className={cn("min-w-0 truncate", event.kind === "error" ? "text-chart-error-ink" : "text-foreground")} title={text}>
				{event.run_id ? (
					<Link to="/runs/$runId" params={{ runId: event.run_id }} className="hover:underline">
						{text}
					</Link>
				) : (
					text
				)}
			</span>
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
