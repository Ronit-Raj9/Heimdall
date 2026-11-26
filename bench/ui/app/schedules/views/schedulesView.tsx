import { PageHeader } from "@/components/shell";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Collapsible, FeatureHero, Pill, SetupCard, Step, type PillTone } from "@/components/inbox/featureHero";
import { BellRing, CalendarClock, Code2, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
	errorMessage,
	isPaused,
	plainScheduleError,
	useGetSchedulesQuery,
	useSetSchedulePausedMutation,
	useRunScheduleMutation,
	useSaveSchedulesMutation,
	useTestAlertMutation,
} from "@/lib/store/apis/schedulesApi";
import type { AlertConfig, Schedule, ScheduleRow, SchedulesFile, SchedulesResponse } from "@/lib/types/schedules";
import { type Evaluation, useListEvaluationsQuery } from "@/lib/store/apis/evaluationsApi";
import { ConfirmButton } from "@/components/platform/badges";
import { type ConfirmFn, useConfirm } from "@/components/platform/confirmDialog";
import { WhyDisabled } from "@/components/ui/whyDisabled";
import { cn } from "@/lib/utils";
import type { TFunction } from "i18next";
import "./w4m7bI18n";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * Run now starts the schedule's evaluation or comparison again and spends money, so it asks first
 * (fix wave 3, N231). Resolves true when the person confirmed. Exported for tests.
 */
export function confirmRunNow(confirm: ConfirmFn, t: TFunction<"schedules">, r: ScheduleRow): Promise<boolean> {
	const what = r.evaluation || r.experiment || r.job || r.name;
	const limit = r.max_usd_per_day ?? 0;
	const message =
		limit > 0
			? t("runNowAsk", { name: r.name, evaluation: what, left: Math.max(0, limit - r.spent_today_usd).toFixed(2), limit: limit.toFixed(2) })
			: t("runNowAskNoLimit", { name: r.name, evaluation: what });
	return confirm(message, { title: t("runNowTitle"), confirmLabel: t("runNow"), cancelLabel: t("runNowCancel") });
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "22 Nov 20:07" in local time, "" for a missing date. */
function shortDate(iso: string): string {
	const d = new Date(iso);
	if (Number.isNaN(d.getTime())) return "";
	const pad = (n: number) => String(n).padStart(2, "0");
	return `${d.getDate()} ${MONTHS[d.getMonth()]} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/**
 * The evaluation picker's options (fix wave 3, N231): "{name} · {benchmarks} · {date}", newest
 * first, with the status of evaluations that are not done; cancelled and failed ones come last,
 * under "Stopped". Two evaluations with one name read differently. Exported for tests.
 */
export function evaluationOptions(
	evaluations: Evaluation[],
	t: TFunction<"schedules">,
): { active: { id: string; label: string }[]; stopped: { id: string; label: string }[] } {
	const sorted = [...evaluations].sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""));
	const label = (ev: Evaluation) => {
		const parts = {
			name: ev.spec?.name || ev.id,
			benchmarks: (ev.spec?.benchmarks ?? []).join(", ") || "-",
			date: shortDate(ev.created_at) || ev.id,
		};
		return ev.status === "done"
			? t("add.evaluationOption", parts)
			: t("add.evaluationOptionStatus", { ...parts, status: t(`add.status.${ev.status}` as never, { defaultValue: ev.status }) });
	};
	const stopped = (ev: Evaluation) => ev.status === "cancelled" || ev.status === "failed";
	return {
		active: sorted.filter((ev) => !stopped(ev)).map((ev) => ({ id: ev.id, label: label(ev) })),
		stopped: sorted.filter(stopped).map((ev) => ({ id: ev.id, label: label(ev) })),
	};
}

/** Whether the add or edit form closes after a save: only when the save worked (p13-5). Exported for tests. */
export function afterSave(ok: boolean): boolean {
	return ok;
}

/** What the add form still needs, in plain words, in form order (p13-7). The server still decides. Exported for tests. */
export function addScheduleMissing(
	s: { name: string; cron: string; max: string },
	what: "evaluation" | "experiment",
	target: string,
	t: TFunction<"w4m7b">,
): string[] {
	const out: string[] = [];
	if (!s.name.trim()) out.push(t("schedules.missing.name"));
	if (!target.trim()) out.push(t(what === "evaluation" ? "schedules.missing.evaluation" : "schedules.missing.experiment"));
	if (!s.cron.trim()) out.push(t("schedules.missing.cron"));
	if (what === "evaluation" && !(Number(s.max) > 0)) out.push(t("schedules.missing.max"));
	return out;
}

/**
 * Schedules: /schedules. Web schedules live in policies/schedules.yaml; scheduled canaries are
 * listed read-only. A server ticker queues each schedule's job at its cron time.
 */
export default function SchedulesView() {
	const { t } = useTranslation("schedules");
	const q = useGetSchedulesQuery();
	const [save, saveState] = useSaveSchedulesMutation();
	const [yamlMode, setYamlMode] = useState(false);
	const [msg, setMsg] = useState<string | null>(null);
	const [formError, setFormError] = useState<string | null>(null);

	// p13-5: resolves whether the save worked. A failed save from the add or edit form is shown in the
	// form, which stays open with what was typed; other failures are shown above the page.
	const persist = async (body: { file?: SchedulesFile; yaml?: string }, fromForm = false): Promise<boolean> => {
		setMsg(null);
		setFormError(null);
		const res = await save(body);
		if ("error" in res && res.error) {
			const text = t("saveError", { error: plainScheduleError(errorMessage(res.error, "unknown error")) });
			if (fromForm) setFormError(text);
			else setMsg(text);
			return false;
		}
		setMsg(t("saved"));
		return true;
	};

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-6" data-testid="schedules-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				actions={
					<Button
						size="sm"
						variant="ghost"
						onClick={() => {
							setMsg(null);
							setFormError(null);
							setYamlMode((v) => !v);
						}}
						data-testid="schedules-yaml-toggle"
					>
						<Code2 className="size-4" aria-hidden="true" />
						{yamlMode ? t("yaml.hide") : t("yaml.show")}
					</Button>
				}
			/>
			{msg && (
				<p className="text-sm" data-testid="schedules-msg">
					{msg}
				</p>
			)}
			{q.isLoading ? (
				<BlockSkeleton rows={4} testId="schedules-loading" />
			) : q.error || !q.data ? (
				<LoadError title={t("loadError")} error={q.error} onRetry={() => void q.refetch()} testId="schedules-error" />
			) : yamlMode ? (
				<YamlEditor initial={q.data.yaml} busy={saveState.isLoading} onSave={(yaml) => void persist({ yaml })} />
			) : (
				<SchedulesBody
					data={q.data}
					busy={saveState.isLoading}
					onSave={(file, fromForm) => persist({ file }, fromForm)}
					formError={formError}
					onFormOpen={() => setFormError(null)}
				/>
			)}
		</section>
	);
}

function YamlEditor({ initial, busy, onSave }: { initial: string; busy: boolean; onSave(yaml: string): void }) {
	const { t } = useTranslation("schedules");
	const [text, setText] = useState(initial || "schedules: []\n");
	return (
		<div className="flex flex-col gap-2">
			<textarea
				className="bg-card min-h-80 w-full rounded-md border p-3 font-mono text-xs"
				value={text}
				onChange={(e) => setText(e.target.value)}
				spellCheck={false}
				data-testid="schedules-yaml"
			/>
			<div>
				<Button size="sm" disabled={busy} onClick={() => onSave(text)} data-testid="schedules-yaml-save">
					{t("yaml.save")}
				</Button>
			</div>
		</div>
	);
}

export interface SchedulesBodyProps {
	data: SchedulesResponse;
	busy: boolean;
	/** Resolves whether the save worked; fromForm marks a save from the add or edit form (p13-5). */
	onSave(file: SchedulesFile, fromForm?: boolean): Promise<boolean> | void;
	/** The failed save of the open add or edit form, shown under its submit button. */
	formError?: string | null;
	onFormOpen?(): void;
}

/** The loaded schedules, the add form and the webhook alerts. Exported for tests. */
export function SchedulesBody({ data, busy, onSave, formError, onFormOpen }: SchedulesBodyProps) {
	const { t } = useTranslation("schedules");
	const { t: u } = useTranslation("ux13");
	const [adding, setAdding] = useState(false);
	const [editing, setEditing] = useState<Schedule | null>(null);
	const file = data.file;
	const saveWith = (patch: Partial<SchedulesFile>, fromForm = false) =>
		onSave({ alerts: file.alerts ?? [], schedules: file.schedules ?? [], ...patch }, fromForm);
	const saveFromForm = async (patch: Partial<SchedulesFile>) => afterSave((await saveWith(patch, true)) !== false);
	return (
		<div className="flex min-w-0 flex-col gap-6">
			{!data.ticker && (
				<p className="text-muted-foreground text-xs" data-testid="schedules-no-ticker">
					{t("noTicker")}{" "}
					<a className="underline" href="/operate">
						{t("noTickerLink")}
					</a>
				</p>
			)}
			{data.hosted && data.limits && (
				<p className="text-muted-foreground text-xs" data-testid="schedules-hosted-limits">
					{t("hostedLimits", { max: data.limits.max_schedules, min: data.limits.min_interval_min, count: data.rows.length })}{" "}
					<a className="underline" href="/workers">
						{t("hostedRunnersLink")}
					</a>
				</p>
			)}
			{data.rows.length === 0 && !adding ? (
				<FeatureHero
					icon={CalendarClock}
					title={u("schedules.heroTitle")}
					message={u("schedules.heroMessage")}
					testId="schedules-empty"
					action={
						<Button
							onClick={() => {
								onFormOpen?.();
								setAdding(true);
							}}
							data-testid="schedules-hero-add"
						>
							{u("schedules.heroAction")}
						</Button>
					}
				/>
			) : (
				data.rows.length > 0 && (
					<section className="flex flex-col gap-3" data-testid="schedules-list">
						<div className="flex items-center justify-between gap-3">
							<h2 className="text-foreground text-base font-semibold tracking-tight">{t("title")}</h2>
							{!adding && (
								<Button
									size="sm"
									onClick={() => {
										onFormOpen?.();
										setAdding(true);
									}}
									data-testid="schedules-add-open"
								>
									<Plus className="size-4" aria-hidden="true" />
									{u("schedules.newAction")}
								</Button>
							)}
						</div>
						<ScheduleTable
							rows={data.rows}
							onRemove={(name) => saveWith({ schedules: file.schedules.filter((s) => s.name !== name) })}
							onEdit={(name) => {
								setAdding(false);
								onFormOpen?.();
								setEditing(file.schedules.find((s) => s.name === name) ?? null);
							}}
							busy={busy}
						/>
					</section>
				)
			)}
			{adding && (
				<SetupCard
					icon={CalendarClock}
					title={t("add.title")}
					testId="schedules-add"
					actions={
						<Button size="sm" variant="ghost" onClick={() => setAdding(false)} data-testid="schedules-add-close">
							{u("schedules.close")}
						</Button>
					}
				>
					<AddScheduleForm
						busy={busy}
						error={formError}
						onAdd={async (s) => {
							if (await saveFromForm({ schedules: [...(file.schedules ?? []), s] })) setAdding(false);
						}}
					/>
				</SetupCard>
			)}
			{editing && (
				<SetupCard
					icon={CalendarClock}
					title={t("editTitle", { name: editing.name })}
					testId="schedules-edit"
					actions={
						<Button size="sm" variant="ghost" onClick={() => setEditing(null)} data-testid="schedules-edit-close">
							{u("schedules.close")}
						</Button>
					}
				>
					<AddScheduleForm
						key={editing.name}
						busy={busy}
						initial={editing}
						error={formError}
						onAdd={async (s) => {
							if (await saveFromForm({ schedules: (file.schedules ?? []).map((x) => (x.name === editing.name ? s : x)) })) setEditing(null);
						}}
					/>
				</SetupCard>
			)}
			<Collapsible icon={BellRing} title={u("schedules.integrations")} hint={u("schedules.integrationsHint")} testId="schedules-alerts">
				<p className="text-muted-foreground text-sm">{t("alerts.description")}</p>
				<AlertsEditor
					alerts={file.alerts ?? []}
					events={data.events}
					secrets={data.secrets}
					busy={busy}
					onChange={(alerts) => saveWith({ alerts })}
				/>
			</Collapsible>
		</div>
	);
}

/** The pill tone for a schedule's last run status. */
function lastTone(status: string): PillTone {
	const s = status.toLowerCase();
	if (s === "ok" || s === "success" || s === "succeeded" || s === "done") return "ok";
	if (s === "running" || s === "queued") return "running";
	if (s === "skipped") return "warn";
	return "error";
}

function ScheduleTable({
	rows,
	onRemove,
	onEdit,
	busy,
}: {
	rows: ScheduleRow[];
	onRemove(name: string): void;
	onEdit(name: string): void;
	busy: boolean;
}) {
	const { t } = useTranslation("schedules");
	const { t: u } = useTranslation("ux13");
	const confirm = useConfirm();
	const [run, runState] = useRunScheduleMutation();
	const [setPaused, pauseState] = useSetSchedulePausedMutation();
	const [note, setNote] = useState<string | null>(null);
	const togglePause = async (name: string, paused: boolean) => {
		setNote(null);
		const res = await setPaused({ name, paused });
		if ("error" in res && res.error) setNote(errorMessage(res.error, "not changed"));
	};
	const runNow = async (row: ScheduleRow) => {
		if (!(await confirmRunNow(confirm, t, row))) return;
		const name = row.name;
		setNote(null);
		const res = await run(name);
		if ("data" in res && res.data) setNote(t("queued", { id: res.data.job_id }));
		else if ("error" in res) setNote(errorMessage(res.error, "not queued"));
	};
	return (
		<div className="flex flex-col gap-2">
			<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
				<table className="w-full text-sm" data-testid="schedules-table">
					<thead className="bg-muted/40 text-muted-foreground sticky top-0 text-left text-xs">
						<tr>
							<th className="px-4 py-2.5 font-medium">{t("table.name")}</th>
							<th className="px-4 py-2.5 font-medium">{t("table.cron")}</th>
							<th className="px-4 py-2.5 font-medium">{t("table.last")}</th>
							<th className="px-4 py-2.5 text-right font-medium">{t("table.spent")}</th>
							<th className="px-4 py-2.5 text-right font-medium">
								<span className="sr-only">{t("table.actions")}</span>
							</th>
						</tr>
					</thead>
					<tbody>
						{rows.map((r) => (
							<tr key={r.name} className="hover:bg-muted/30 border-t align-middle transition-colors" data-testid={`schedule-row-${r.name}`}>
								<td className="max-w-64 px-4 py-3">
									<div className="flex items-center gap-2">
										<span className="truncate font-medium" title={r.name}>
											{r.name}
										</span>
										{isPaused(r) && (
											<Pill tone="muted" testId={`schedule-paused-${r.name}`}>
												{t("paused")}
											</Pill>
										)}
									</div>
									<div className="text-muted-foreground truncate text-xs" title={r.job || r.evaluation || r.experiment}>
										{t(`kind.${r.kind}` as never, { defaultValue: r.kind })}
										{r.job || r.evaluation || r.experiment ? ` · ${r.job || r.evaluation || r.experiment}` : ""}
									</div>
								</td>
								<td className="px-4 py-3 whitespace-nowrap">
									<div className="font-mono text-xs">
										{r.cron}
										{r.timezone ? ` ${r.timezone}` : ""}
									</div>
									<div className="text-muted-foreground text-xs">
										{isPaused(r) ? t("paused") : r.next ? new Date(r.next).toLocaleString() : "-"}
									</div>
								</td>
								<td className="px-4 py-3">
									{r.problem ? (
										<Pill tone="warn" title={r.problem}>
											{u("schedules.pillProblem")}
										</Pill>
									) : r.last ? (
										<Pill tone={lastTone(r.last.status)} title={r.last.reason}>
											{r.last.status}
										</Pill>
									) : (
										<Pill tone="muted">{t("never")}</Pill>
									)}
								</td>
								<td className="px-4 py-3 text-right text-xs whitespace-nowrap tabular-nums">
									{r.spent_today_usd.toFixed(2)}
									{r.max_usd_per_day ? ` / ${r.max_usd_per_day.toFixed(2)}` : ""}
								</td>
								<td className="px-4 py-3">
									<div className="flex justify-end gap-2">
										<Button
											size="sm"
											variant="outline"
											disabled={runState.isLoading}
											onClick={() => void runNow(r)}
											data-testid={`schedule-run-${r.name}`}
										>
											{t("runNow")}
										</Button>
										<Button
											size="sm"
											variant="outline"
											disabled={pauseState.isLoading}
											onClick={() => void togglePause(r.name, !isPaused(r))}
											data-testid={`schedule-${isPaused(r) ? "resume" : "pause"}-${r.name}`}
										>
											{isPaused(r) ? t("resume") : t("pause")}
										</Button>
										{r.source === "web" ? (
											<>
												<Button
													size="sm"
													variant="outline"
													disabled={busy}
													onClick={() => onEdit(r.name)}
													data-testid={`schedule-edit-${r.name}`}
												>
													{t("canary.edit")}
												</Button>
												<ConfirmButton
													label={t("remove")}
													question={t("removeAsk", { name: r.name })}
													confirmLabel={t("removeConfirm")}
													cancelLabel={t("removeCancel")}
													onConfirm={() => onRemove(r.name)}
													disabled={busy}
													variant="destructive"
													testId={`schedule-remove-${r.name}`}
												/>
											</>
										) : (
											<span className="text-muted-foreground self-center text-xs whitespace-nowrap">{t("fromCanary")}</span>
										)}
									</div>
								</td>
							</tr>
						))}
					</tbody>
				</table>
			</div>
			{note && (
				<p className="text-xs" data-testid="schedules-run-note">
					{note}
				</p>
			)}
		</div>
	);
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<label className="flex flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{label}</span>
			{children}
		</label>
	);
}

/** A schedule edited in the add form: the form's fields over the saved one, so what the form does not show is kept. Exported for tests. */
export function mergeScheduleEdit(initial: Schedule | undefined, next: Schedule): Schedule {
	if (!initial) return next;
	const base: Schedule = { ...initial };
	delete base.experiment;
	delete base.evaluation;
	delete base.timezone;
	delete base.max_usd_per_day;
	return { ...base, ...next };
}

function AddScheduleForm({
	busy,
	onAdd,
	initial,
	error,
}: {
	busy: boolean;
	onAdd(s: Schedule): void | Promise<void>;
	initial?: Schedule;
	error?: string | null;
}) {
	const { t } = useTranslation("schedules");
	const { t: w } = useTranslation("w4m7b");
	const { t: u } = useTranslation("ux13");
	const evaluations = useListEvaluationsQuery();
	const options = evaluationOptions(evaluations.data ?? [], t);
	const [what, setWhat] = useState<"evaluation" | "experiment">(initial?.experiment ? "experiment" : "evaluation");
	const [s, setS] = useState({
		name: initial?.name ?? "",
		experiment: initial?.experiment ?? "",
		evaluation: initial?.evaluation ?? "",
		cron: initial?.cron ?? "0 6 * * *",
		timezone: initial?.timezone ?? "",
		max: initial?.max_usd_per_day ? String(initial.max_usd_per_day) : "",
	});
	const set = (k: keyof typeof s) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setS({ ...s, [k]: e.target.value });
	const target = what === "evaluation" ? s.evaluation : s.experiment;
	const missing = addScheduleMissing(s, what, target, w);
	const ok = missing.length === 0;
	const why = ok ? null : w("schedules.toAdd", { list: missing.join(", ") });
	return (
		<form
			className="flex flex-col"
			onSubmit={(e) => {
				e.preventDefault();
				if (!ok) return;
				const max = Number(s.max);
				void onAdd(
					mergeScheduleEdit(initial, {
						name: s.name.trim(),
						...(what === "evaluation" ? { evaluation: s.evaluation.trim() } : { experiment: s.experiment.trim() }),
						cron: s.cron.trim(),
						...(s.timezone ? { timezone: s.timezone.trim() } : {}),
						...(max > 0 ? { max_usd_per_day: max } : {}),
					}),
				);
			}}
			data-testid="schedule-add-form"
		>
			<Step n={1} title={u("schedules.stepWhat")}>
				<div className="bg-muted inline-flex w-fit rounded-lg p-1 text-xs" role="tablist">
					{(["evaluation", "experiment"] as const).map((k) => (
						<button
							key={k}
							type="button"
							role="tab"
							aria-selected={what === k}
							className={cn("rounded-md px-3 py-1 font-medium", what === k ? "bg-background shadow-sm" : "text-muted-foreground")}
							onClick={() => setWhat(k)}
							data-testid={`schedule-add-what-${k}`}
						>
							{t(k === "evaluation" ? "add.whatEvaluation" : "add.whatExperiment")}
						</button>
					))}
				</div>
				<div className="grid gap-3 sm:grid-cols-2">
					<Field label={t("add.name")}>
						<Input value={s.name} onChange={set("name")} placeholder={t("add.namePlaceholder")} data-testid="schedule-add-name" />
					</Field>
					{what === "evaluation" ? (
						<Field label={t("add.evaluation")}>
							<select
								className="border-input bg-background h-9 rounded-md border px-2 text-sm"
								value={s.evaluation}
								onChange={set("evaluation")}
								data-testid="schedule-add-evaluation"
							>
								<option value="">{t("add.evaluationPlaceholder")}</option>
								{options.active.map((o) => (
									<option key={o.id} value={o.id}>
										{o.label}
									</option>
								))}
								{options.stopped.length > 0 && (
									<optgroup label={t("add.stopped")}>
										{options.stopped.map((o) => (
											<option key={o.id} value={o.id}>
												{o.label}
											</option>
										))}
									</optgroup>
								)}
							</select>
						</Field>
					) : (
						<Field label={t("add.experiment")}>
							<Input
								value={s.experiment}
								onChange={set("experiment")}
								placeholder={t("add.experimentPlaceholder")}
								data-testid="schedule-add-experiment"
							/>
						</Field>
					)}
				</div>
				{what === "evaluation" && <p className="text-muted-foreground text-xs">{t("add.evaluationHint")}</p>}
			</Step>
			<Step n={2} title={u("schedules.stepWhen")}>
				<div className="grid gap-3 sm:grid-cols-2">
					<Field label={t("add.cron")}>
						<Input value={s.cron} onChange={set("cron")} className="font-mono" data-testid="schedule-add-cron" />
					</Field>
					<Field label={t("add.timezone")}>
						<Input value={s.timezone} onChange={set("timezone")} placeholder={t("add.timezonePlaceholder")} />
					</Field>
				</div>
				<p className="text-muted-foreground text-xs">{t("add.hint")}</p>
			</Step>
			<Step n={3} title={u("schedules.stepCap")} last>
				<div className="grid gap-3 sm:grid-cols-2">
					<Field label={t("add.maxUsd")}>
						<Input value={s.max} onChange={set("max")} inputMode="decimal" data-testid="schedule-add-max" />
					</Field>
				</div>
				{what === "experiment" && s.max.trim() === "" && (
					<p className="text-muted-foreground text-xs" data-testid="schedule-add-limit-hint">
						{w("schedules.limitHint")}
					</p>
				)}
				<div className="flex flex-col gap-1">
					<Button
						type="submit"
						className="w-fit"
						disabled={busy || !ok}
						aria-describedby={why ? "schedule-add-why" : undefined}
						data-testid="schedule-add-submit"
					>
						{initial ? t("canary.save") : t("add.submit")}
					</Button>
					<WhyDisabled reason={busy ? null : why} testId="schedule-add-why" />
					{error && (
						<p className="text-destructive text-xs" role="alert" data-testid="schedule-form-error">
							{error}
						</p>
					)}
				</div>
			</Step>
		</form>
	);
}

export interface AlertsEditorProps {
	alerts: AlertConfig[];
	events: string[];
	secrets: string[];
	busy: boolean;
	onChange(alerts: AlertConfig[]): void;
}

/** File-level webhook receivers with a test button. Exported for tests. */
export function AlertsEditor({ alerts, events, secrets, busy, onChange }: AlertsEditorProps) {
	const { t } = useTranslation("schedules");
	const { t: w } = useTranslation("w4m7b");
	const [test, testState] = useTestAlertMutation();
	const [results, setResults] = useState<Record<string, string>>({});
	const [draft, setDraft] = useState<AlertConfig>({ url_env: secrets[0] ?? "", format: "" });
	const sendTest = async (a: AlertConfig) => {
		const res = await test({ url_env: a.url_env, secret_env: a.secret_env, format: a.format });
		const out =
			"data" in res && res.data
				? res.data.delivered
					? t("alerts.testOk")
					: t("alerts.testFail", { error: res.data.error ?? "" })
				: t("alerts.testFail", { error: errorMessage("error" in res ? res.error : null, "request failed") });
		setResults((r) => ({ ...r, [a.url_env]: out }));
	};
	const toggleEvent = (e: string) => {
		const cur = draft.events ?? [];
		setDraft({ ...draft, events: cur.includes(e) ? cur.filter((x) => x !== e) : [...cur, e] });
	};
	return (
		<div className="flex flex-col gap-4">
			{alerts.length === 0 ? (
				<EmptyNote testId="alerts-empty">{t("alerts.empty")}</EmptyNote>
			) : (
				<ul className="flex flex-col gap-2" data-testid="alerts-list">
					{alerts.map((a, i) => (
						<li key={`${a.url_env}-${i}`} className="flex flex-wrap items-center gap-3 text-sm">
							<span className="font-mono">{a.url_env}</span>
							<span className="text-muted-foreground text-xs">
								{a.format || "json"} · {a.events?.length ? a.events.join(", ") : "all events"}
							</span>
							<Button
								size="sm"
								variant="outline"
								disabled={testState.isLoading}
								onClick={() => void sendTest(a)}
								data-testid={`alert-test-${i}`}
							>
								{t("alerts.test")}
							</Button>
							<ConfirmButton
								label={t("remove")}
								question={w("schedules.alerts.removeAsk", { name: a.url_env })}
								confirmLabel={w("schedules.alerts.removeConfirm")}
								cancelLabel={t("removeCancel")}
								variant="destructive"
								disabled={busy}
								testId={`alert-remove-${i}`}
								onConfirm={() => onChange(alerts.filter((_, j) => j !== i))}
							/>
							{results[a.url_env] && <span className="text-xs">{results[a.url_env]}</span>}
						</li>
					))}
				</ul>
			)}
			{secrets.length === 0 && (
				<p className="text-muted-foreground text-xs" data-testid="alerts-no-secrets">
					{t("alerts.noSecrets")}
				</p>
			)}
			<div className="grid gap-3 md:grid-cols-3">
				<Field label={t("alerts.urlEnv")}>
					<Input
						list="schedule-secret-names"
						value={draft.url_env}
						onChange={(e) => setDraft({ ...draft, url_env: e.target.value })}
						placeholder={t("alerts.urlEnvPlaceholder")}
						data-testid="alert-url-env"
					/>
				</Field>
				<Field label={t("alerts.secretEnv")}>
					<Input
						list="schedule-secret-names"
						value={draft.secret_env ?? ""}
						onChange={(e) => setDraft({ ...draft, secret_env: e.target.value || undefined })}
					/>
				</Field>
				<Field label={t("alerts.format")}>
					<select
						className="bg-card h-9 rounded-md border px-2 text-sm"
						value={draft.format ?? ""}
						onChange={(e) => setDraft({ ...draft, format: e.target.value as AlertConfig["format"] })}
					>
						<option value="">json</option>
						<option value="slack">slack</option>
					</select>
				</Field>
				<datalist id="schedule-secret-names">
					{secrets.map((s) => (
						<option key={s} value={s} />
					))}
				</datalist>
			</div>
			<fieldset className="flex flex-wrap gap-3 text-xs">
				<legend className="text-muted-foreground mb-1">{t("alerts.events")}</legend>
				{events.map((e) => (
					<label key={e} className="flex items-center gap-1">
						<input type="checkbox" checked={draft.events?.includes(e) ?? false} onChange={() => toggleEvent(e)} />
						{e}
					</label>
				))}
			</fieldset>
			<div className="flex gap-2">
				<Button
					size="sm"
					disabled={busy || !draft.url_env}
					onClick={() => {
						const next: AlertConfig = { url_env: draft.url_env.trim() };
						if (draft.secret_env) next.secret_env = draft.secret_env.trim();
						if (draft.format) next.format = draft.format;
						if (draft.events?.length) next.events = draft.events;
						onChange([...alerts, next]);
					}}
					data-testid="alert-add"
				>
					{t("alerts.add")}
				</Button>
				<Button
					size="sm"
					variant="outline"
					disabled={testState.isLoading || !draft.url_env}
					onClick={() => void sendTest(draft)}
					data-testid="alert-test-draft"
				>
					{t("alerts.test")}
				</Button>
				{draft.url_env && results[draft.url_env] && <span className="self-center text-xs">{results[draft.url_env]}</span>}
			</div>
		</div>
	);
}