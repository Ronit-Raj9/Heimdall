import { InlineError, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { PaidButton } from "@/components/ui/paidButton";
import { formatUsd } from "@/lib/money";
import { useGetModelWatchQuery, useRetryModelWatchMutation } from "@/lib/store/apis/gatewayApi";
import { useGetSchedulesQuery, useRunScheduleMutation } from "@/lib/store/apis/schedulesApi";
import type { GwWatchItem, GwWatchState } from "@/lib/types/gateway";
import type { ScheduleRow } from "@/lib/types/schedules";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { NewModelWatchForm } from "./modelWatchForm";
import "./w4intI18n";
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
export function ModelWatchView({
	row,
	state,
	onRetry,
	onStartNow,
}: {
	row: WatchScheduleRow;
	state?: GwWatchState;
	onRetry(model: string): void | Promise<unknown>;
	/** "Start now" on a skipped model: clear it, then run the schedule now. Defaults to onRetry. */
	onStartNow?(model: string): void | Promise<unknown>;
}) {
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
					usd: formatUsd(w.usd_per_model),
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
												{/* Both start a paid evaluation of this model (M7-113): say so and ask first. */}
												<PaidButton
													size="sm"
													variant="outline"
													verb={it.state === "failed" ? t("watch.tryAgain") : t("watch.startNow")}
													capUsd={w.usd_per_model}
													confirm="always"
													estimateAt="title"
													onConfirm={() => (it.state === "skipped" && onStartNow ? onStartNow(key) : onRetry(key))}
													testId={`model-watch-retry-${key}`}
												/>
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

/** Start now: the model is cleared first, so the schedule's run picks it up (a skipped model stays seen until then). */
export async function startNow(
	retry: (arg: { schedule: string; model: string }) => Promise<unknown>,
	run: (schedule: string) => Promise<unknown>,
	schedule: string,
	model: string,
) {
	await retry({ schedule, model });
	await run(schedule);
}

function WatchFor({ row }: { row: WatchScheduleRow }) {
	const q = useGetModelWatchQuery(row.name);
	const [retry] = useRetryModelWatchMutation();
	const [run] = useRunScheduleMutation();
	const [error, setError] = useState<unknown>(null);
	// Every action reports its failure next to the watch (M1-44), never silently.
	const attempt = async (fn: () => Promise<unknown>) => {
		setError(null);
		try {
			await fn();
		} catch (e) {
			setError(e);
		}
	};
	return (
		<div className="flex flex-col gap-2">
			<ModelWatchView
				row={row}
				state={q.data}
				onRetry={(model) => attempt(() => retry({ schedule: row.name, model }).unwrap())}
				onStartNow={(model) =>
					attempt(() =>
						startNow(
							(a) => retry(a).unwrap(),
							(s) => run(s).unwrap(),
							row.name,
							model,
						),
					)
				}
			/>
			<InlineError error={error} testId={`model-watch-error-${row.name}`} />
		</div>
	);
}

/** Gateway page: "Watch for new models", one block per schedule with a new model watch. */
export function ModelWatchCard() {
	const { t } = useTranslation("w4p6");
	const { t: ti } = useTranslation("w4int");
	const [adding, setAdding] = useState(false);
	const q = useGetSchedulesQuery();
	const rows = ((q.data?.rows ?? []) as WatchScheduleRow[]).filter((r) => r.new_models);
	return (
		<Section title={t("watch.title")} testId="model-watch-card">
			{q.isError ? (
				<LoadError title={t("watch.title")} error={q.error} onRetry={() => void q.refetch()} testId="model-watch-load-error" />
			) : q.isLoading ? null : rows.length === 0 ? (
				<p className="text-muted-foreground text-sm">{t("watch.none")}</p>
			) : (
				rows.map((r) => <WatchFor key={r.name} row={r} />)
			)}
			{adding ? (
				<NewModelWatchForm onDone={() => setAdding(false)} />
			) : (
				<div>
					<Button size="sm" variant="outline" onClick={() => setAdding(true)} data-testid="model-watch-add">
						{ti("watchForm.open")}
					</Button>
				</div>
			)}
		</Section>
	);
}