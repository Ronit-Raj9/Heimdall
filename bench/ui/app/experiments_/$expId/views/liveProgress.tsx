import { EXPERIMENT_POLL_MS, useGetGridQuery } from "@/lib/store/apis/benchApi";
import { useGetExperimentEventsQuery } from "@/lib/store/apis/homeApi";
import { formatValueNumber } from "@/components/valueCell.utils";
import { getLocale } from "@/lib/i18n";
import type { ExperimentStatus, LiveEvent } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

type SlotState = "pending" | "running" | "pass" | "fail" | "error";

const SLOT_CLASS: Record<SlotState, string> = {
	pending: "bg-muted",
	running: "bg-chart-info animate-pulse",
	pass: "bg-chart-success",
	fail: "bg-chart-error",
	error: "bg-chart-warning",
};

const MAX_EVENTS = 6;

/**
 * While an experiment runs: a progress bar, one square per slot coloured by its state, and the
 * latest events. Polls the JSON form of GET /experiments/{id}/events and the grid.
 */
export function LiveProgress({ status }: { status: ExperimentStatus }) {
	const { t } = useTranslation("experimentPage");
	const locale = getLocale();
	const expId = status.exp_id;
	const events = useGetExperimentEventsQuery({ expId, since: 0 }, { pollingInterval: EXPERIMENT_POLL_MS, skipPollingIfUnfocused: true });
	const grid = useGetGridQuery(expId);
	const live = events.data?.status ?? status;
	const { total, done, running, failed } = live.slots;
	const share = total > 0 ? Math.min(100, (done / total) * 100) : 0;
	const list = events.data?.events ?? [];
	const slots = slotStates(grid.data?.rows.flatMap((row) => row.cells) ?? [], list);
	const latest = list.slice(-MAX_EVENTS).reverse();
	const spent = live.spent_usd?.state === "observed" && typeof live.spent_usd.v === "number" ? formatValueNumber(live.spent_usd.v, "usd", locale) : null;

	return (
		<section className="bg-card flex flex-col gap-4 rounded-xl border p-5" data-testid="experiment-progress" data-state={live.state}>
			<div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
				<span className="flex items-center gap-2 font-semibold">
					<span className="bg-chart-info size-2 animate-pulse rounded-full" aria-hidden="true" />
					{t("live.title")}
				</span>
				<span className="tabular-nums">{t("live.slots", { done, total })}</span>
				{running > 0 && <span className="text-muted-foreground">{t("live.running", { count: running })}</span>}
				{failed > 0 && <span className="text-chart-error-ink">{t("live.failed", { count: failed })}</span>}
				{spent && <span className="text-muted-foreground ml-auto tabular-nums">{t("live.spent", { usd: spent })}</span>}
			</div>
			<div className="bg-muted h-2 w-full overflow-hidden rounded-full" role="progressbar" aria-valuemin={0} aria-valuemax={total} aria-valuenow={done}>
				<div className="bg-chart-info h-full rounded-full transition-all" style={{ width: `${share}%` }} />
			</div>
			<div className="grid grid-cols-1 gap-4 md:grid-cols-2">
				{slots.length > 0 && (
					<div className="flex flex-col gap-2">
						<h3 className="text-muted-foreground text-xs font-medium">{t("live.slotsTitle")}</h3>
						<ul className="flex flex-wrap gap-1" data-testid="experiment-progress-slots">
							{slots.map(({ id, state }) => (
								<li
									key={id}
									className={cn("size-3 rounded-sm", SLOT_CLASS[state])}
									title={`${id}: ${t(`live.slot.${state}`)}`}
									aria-label={`${id}: ${t(`live.slot.${state}`)}`}
									data-state={state}
								/>
							))}
						</ul>
					</div>
				)}
				<div className="flex min-w-0 flex-col gap-2">
					<h3 className="text-muted-foreground text-xs font-medium">{t("live.eventsTitle")}</h3>
					{latest.length === 0 ? (
						<p className="text-muted-foreground text-xs">{t("live.noEvents")}</p>
					) : (
						<ul className="flex flex-col gap-1 text-xs" data-testid="experiment-progress-events">
							{latest.map((event) => (
								<li key={event.seq} className="flex min-w-0 gap-2">
									<span className="text-muted-foreground shrink-0 tabular-nums">{timeOf(event.at, locale)}</span>
									<span className="truncate" title={eventLine(event)}>
										{eventLine(event)}
									</span>
								</li>
							))}
						</ul>
					)}
				</div>
			</div>
		</section>
	);
}

function slotStates(cells: { slot_id: string; outcome: string | null }[], events: LiveEvent[]): { id: string; state: SlotState }[] {
	const started = new Set<string>();
	for (const event of events) if (event.kind === "slot_started" && event.slot_id) started.add(event.slot_id);
	return cells.map((cell) => {
		if (cell.outcome === "pass") return { id: cell.slot_id, state: "pass" };
		if (cell.outcome === "fail") return { id: cell.slot_id, state: "fail" };
		if (cell.outcome) return { id: cell.slot_id, state: "error" };
		return { id: cell.slot_id, state: started.has(cell.slot_id) ? "running" : "pending" };
	});
}

function eventLine(event: LiveEvent): string {
	const parts = [event.kind.replace(/_/g, " ")];
	if (event.slot_id) parts.push(event.slot_id);
	if (event.stage) parts.push(String(event.stage));
	if (event.outcome) parts.push(String(event.outcome));
	if (event.message) parts.push(event.message);
	return parts.join(" · ");
}

function timeOf(at: string, locale: string): string {
	const d = new Date(at);
	return Number.isNaN(d.getTime()) ? "" : d.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}
