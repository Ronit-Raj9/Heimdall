import { Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useGetModelWatchQuery, useRetryModelWatchMutation } from "@/lib/store/apis/gatewayApi";
import { useGetSchedulesQuery } from "@/lib/store/apis/schedulesApi";
import type { GwWatchItem, GwWatchState } from "@/lib/types/gateway";
import type { ScheduleRow } from "@/lib/types/schedules";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import "./w4p6I18n";

/** The schedule's new model watch (schedule.NewModelWatch, D3 6.4), read through a local type. */
export interface NewModelWatchFields {
	providers: string[];
	current: string;
	match?: string;
	max_per_day?: number;
	usd_per_model: number;
}
export type WatchScheduleRow = ScheduleRow & { new_models?: NewModelWatchFields };

/** A cron line in plain words: daily and hourly, else the line itself. Pure. */
export function cronWords(cron: string): string {
	const f = cron.trim().split(/\s+/);
	if (f.length === 5 && /^\d+$/.test(f[0]) && f.slice(2).every((x) => x === "*")) {
		if (f[1] === "*") return "every hour";
		if (/^\d+$/.test(f[1])) return `every day at ${f[1].padStart(2, "0")}:${f[0].padStart(2, "0")}`;
	}
	return `on the schedule ${cron.trim()}`;
}

/** Newest first. Pure. */
export function watchItems(state: GwWatchState): GwWatchItem[] {
	return Object.values(state.seen ?? {}).sort((a, b) => b.first_seen.localeCompare(a.first_seen));
}

/** One watch: its sentence, the first-check line and the table of models found. Pure. */
export function ModelWatchView({ row, state, onRetry }: { row: WatchScheduleRow; state?: GwWatchState; onRetry(model: string): void }) {
	const { t } = useTranslation("w4p6");
	const w = row.new_models as NewModelWatchFields;
	const items = state ? watchItems(state) : [];
	return (
		<div className="flex flex-col gap-2 text-sm" data-testid={`model-watch-${row.name}`}>
			<p>
				{t("watch.text", {
					schedule: cronWords(row.cron),
					evaluation: row.evaluation ?? row.name,
					current: w.current,
					usd: w.usd_per_model.toFixed(2),
				})}
			</p>
			{items.length === 0 && <p className="text-muted-foreground">{t("watch.firstCheck")}</p>}
			{state?.last_error && <p className="text-amber-700 dark:text-amber-400">{t("watch.lastError", { error: state.last_error })}</p>}
			{items.length > 0 && (
				<table className="w-full text-sm">
					<thead className="text-muted-foreground text-left text-xs">
						<tr>
							<th className="py-1 pr-3 font-normal">{t("watch.model")}</th>
							<th className="py-1 pr-3 font-normal">{t("watch.found")}</th>
							<th className="py-1 font-normal">{t("watch.happened")}</th>
						</tr>
					</thead>
					<tbody>
						{items.map((it) => {
							const key = `${it.provider}/${it.model}`;
							return (
								<tr key={key} className="border-t" data-testid={`model-watch-item-${key}`} data-state={it.state}>
									<td className="py-1 pr-3">{key}</td>
									<td className="py-1 pr-3">{it.first_seen.slice(0, 10)}</td>
									<td className="py-1">
										{it.state === "started" && (
											<span className="flex gap-2">
												{t("watch.started")}
												{it.evaluation_id && (
													<Link to="/evaluations/$evalId" params={{ evalId: it.evaluation_id }} className="text-primary hover:underline">
														{t("watch.openEvaluation")}
													</Link>
												)}
											</span>
										)}
										{it.state === "claimed" && t("watch.claimed")}
										{(it.state === "failed" || it.state === "skipped") && (
											<span className="flex flex-wrap items-center gap-2">
												{t(it.state === "failed" ? "watch.failed" : "watch.skipped", { reason: it.reason ?? "" })}
												<Button type="button" size="sm" variant="outline" onClick={() => onRetry(key)}>
													{it.state === "failed" ? t("watch.tryAgain") : t("watch.startNow")}
												</Button>
											</span>
										)}
									</td>
								</tr>
							);
						})}
					</tbody>
				</table>
			)}
		</div>
	);
}

function WatchFor({ row }: { row: WatchScheduleRow }) {
	const q = useGetModelWatchQuery(row.name);
	const [retry] = useRetryModelWatchMutation();
	return <ModelWatchView row={row} state={q.data} onRetry={(model) => void retry({ schedule: row.name, model })} />;
}

/** Gateway page: "Watch for new models", one block per schedule with a new model watch. */
export function ModelWatchCard() {
	const { t } = useTranslation("w4p6");
	const q = useGetSchedulesQuery();
	const rows = ((q.data?.rows ?? []) as WatchScheduleRow[]).filter((r) => r.new_models);
	return (
		<Section title={t("watch.title")} testId="model-watch-card">
			{rows.length === 0 ? (
				<p className="text-muted-foreground text-sm">{t("watch.none")}</p>
			) : (
				rows.map((r) => <WatchFor key={r.name} row={r} />)
			)}
		</Section>
	);
}