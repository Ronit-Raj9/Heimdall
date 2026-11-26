import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useListEvaluationsQuery, type Evaluation } from "@/lib/store/apis/evaluationsApi";
import { errorMessage, useGetSchedulesQuery, useSaveSchedulesMutation } from "@/lib/store/apis/schedulesApi";
import type { Schedule, SchedulesFile } from "@/lib/types/schedules";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import type { NewModelWatchFields } from "./modelWatchCard";
import "./w4intI18n";

/** The form of a new model watch (D3 6.8), as typed. */
export interface WatchForm {
	providers: string;
	evaluation: string;
	current: string;
	usdPerModel: string;
	maxPerDay: string;
	match: string;
	check: "daily" | "hourly";
	maxUsdPerDay: string;
}

export const EMPTY_WATCH_FORM: WatchForm = {
	providers: "",
	evaluation: "",
	current: "",
	usdPerModel: "",
	maxPerDay: "1",
	match: "",
	check: "daily",
	maxUsdPerDay: "",
};

/** The cron line of each check frequency: daily at 06:00, or at the start of every hour. */
export const WATCH_CRON = { daily: "0 6 * * *", hourly: "0 * * * *" } as const;

/** What is wrong with a form, as a key of the w4int namespace, or the schedule to save. Pure. */
export type WatchBuild =
	| { schedule: Schedule & { new_models: NewModelWatchFields }; problem?: undefined }
	| { schedule?: undefined; problem: WatchProblem };
export type WatchProblem =
	| "needProviders"
	| "tooManyProviders"
	| "needEvaluation"
	| "needCurrent"
	| "needBudget"
	| "needDaily"
	| "dailyTooLow";

/** A schedule name not taken yet: new-models-<evaluation id>, then -2, -3 and so on. Pure. */
export function watchName(evaluation: string, taken: readonly string[]): string {
	// The schedule name rule (schedule/file.go namePattern): lowercase, digits, _ and -, at most 64 characters.
	const base = `new-models-${evaluation}`
		.toLowerCase()
		.replace(/[^a-z0-9_-]/g, "-")
		.slice(0, 58);
	if (!taken.includes(base)) return base;
	for (let i = 2; ; i++) if (!taken.includes(`${base}-${i}`)) return `${base}-${i}`;
}

/** The schedule a form describes: an evaluation schedule with its new model watch. Pure. */
export function watchSchedule(f: WatchForm, taken: readonly string[]): WatchBuild {
	const providers = f.providers
		.split(/[\s,]+/)
		.map((p) => p.trim().toLowerCase())
		.filter((p, i, all) => p !== "" && all.indexOf(p) === i);
	if (providers.length === 0) return { problem: "needProviders" };
	if (providers.length > 5) return { problem: "tooManyProviders" };
	if (!f.evaluation) return { problem: "needEvaluation" };
	if (!f.current) return { problem: "needCurrent" };
	const usd = Number(f.usdPerModel);
	if (!Number.isFinite(usd) || usd <= 0) return { problem: "needBudget" };
	const perDay = Math.min(3, Math.max(1, Math.round(Number(f.maxPerDay) || 1)));
	const daily = Number(f.maxUsdPerDay);
	if (!Number.isFinite(daily) || daily <= 0) return { problem: "needDaily" };
	if (usd * perDay > daily + 1e-9) return { problem: "dailyTooLow" };
	const match = f.match.trim();
	return {
		schedule: {
			name: watchName(f.evaluation, taken),
			evaluation: f.evaluation,
			cron: WATCH_CRON[f.check],
			max_usd_per_day: daily,
			new_models: { providers, current: f.current, usd_per_model: usd, max_per_day: perDay, ...(match ? { match } : {}) },
		},
	};
}

/** The schedules file with the new watch added last; every other schedule and the alerts stay as they are. Pure. */
export function withWatch(file: SchedulesFile, schedule: Schedule): SchedulesFile {
	return { ...file, schedules: [...file.schedules, schedule] };
}

/** The arm labels of a saved evaluation: the setups "Your current model" can name. Pure. */
export function setupLabels(ev: Evaluation | undefined): string[] {
	return (ev?.spec.arms ?? []).map((a) => a.label).filter((l) => l !== "");
}

function Row({ label, hint, children }: { label: string; hint?: string; children: (id: string) => React.ReactNode }) {
	const id = useId();
	return (
		<div className="flex min-w-0 flex-col gap-1 text-sm">
			<label htmlFor={id}>{label}</label>
			{children(id)}
			{hint && <span className="text-muted-foreground text-xs">{hint}</span>}
		</div>
	);
}

const selectClass = "h-9 rounded-md border px-2 text-sm";

/** The fields of the form. Pure apart from the change callback. */
export function WatchFormFields({
	form,
	onChange,
	evaluations,
}: {
	form: WatchForm;
	onChange(f: WatchForm): void;
	evaluations: readonly Evaluation[];
}) {
	const { t } = useTranslation("w4int");
	const set = <K extends keyof WatchForm>(k: K, v: WatchForm[K]) => onChange({ ...form, [k]: v });
	const picked = evaluations.find((e) => e.id === form.evaluation);
	return (
		<div className="grid min-w-0 gap-3 sm:grid-cols-2" data-testid="model-watch-form">
			<Row label={t("watchForm.providers")} hint={t("watchForm.providersHint")}>
				{(id) => (
					<Input id={id} value={form.providers} onChange={(e) => set("providers", e.target.value)} data-testid="model-watch-providers" />
				)}
			</Row>
			<Row label={t("watchForm.evaluation")}>
				{(id) => (
					<select
						id={id}
						className={selectClass}
						value={form.evaluation}
						onChange={(e) => onChange({ ...form, evaluation: e.target.value, current: "" })}
						data-testid="model-watch-evaluation"
					>
						<option value="">{t("watchForm.pick")}</option>
						{evaluations.map((e) => (
							<option key={e.id} value={e.id}>
								{e.spec.name || e.id}
							</option>
						))}
					</select>
				)}
			</Row>
			<Row label={t("watchForm.current")}>
				{(id) => (
					<select
						id={id}
						className={selectClass}
						value={form.current}
						onChange={(e) => set("current", e.target.value)}
						disabled={!picked}
						data-testid="model-watch-current"
					>
						<option value="">{t("watchForm.pick")}</option>
						{setupLabels(picked).map((l) => (
							<option key={l} value={l}>
								{l}
							</option>
						))}
					</select>
				)}
			</Row>
			<Row label={t("watchForm.usdPerModel")}>
				{(id) => (
					<Input
						id={id}
						type="number"
						min={0}
						step="0.5"
						value={form.usdPerModel}
						onChange={(e) => set("usdPerModel", e.target.value)}
						data-testid="model-watch-usd"
					/>
				)}
			</Row>
			<Row label={t("watchForm.maxPerDay")}>
				{(id) => (
					<select id={id} className={selectClass} value={form.maxPerDay} onChange={(e) => set("maxPerDay", e.target.value)}>
						{["1", "2", "3"].map((n) => (
							<option key={n} value={n}>
								{n}
							</option>
						))}
					</select>
				)}
			</Row>
			<Row label={t("watchForm.match")}>{(id) => <Input id={id} value={form.match} onChange={(e) => set("match", e.target.value)} />}</Row>
			<Row label={t("watchForm.check")}>
				{(id) => (
					<select id={id} className={selectClass} value={form.check} onChange={(e) => set("check", e.target.value as WatchForm["check"])}>
						<option value="daily">{t("watchForm.daily")}</option>
						<option value="hourly">{t("watchForm.hourly")}</option>
					</select>
				)}
			</Row>
			<Row label={t("watchForm.maxUsdPerDay")} hint={t("watchForm.maxUsdPerDayHint")}>
				{(id) => (
					<Input
						id={id}
						type="number"
						min={0}
						step="0.5"
						value={form.maxUsdPerDay}
						onChange={(e) => set("maxUsdPerDay", e.target.value)}
						data-testid="model-watch-daily"
					/>
				)}
			</Row>
		</div>
	);
}

/** "Set up a watch": the form, saved as a new evaluation schedule; the server's refusal is shown as it is. */
export function NewModelWatchForm({ onDone }: { onDone(): void }) {
	const { t } = useTranslation("w4int");
	const [form, setForm] = useState<WatchForm>(EMPTY_WATCH_FORM);
	const [error, setError] = useState("");
	const schedules = useGetSchedulesQuery();
	const evaluations = useListEvaluationsQuery();
	const [save, saving] = useSaveSchedulesMutation();
	const file = schedules.data?.file;
	const taken = [...(file?.schedules ?? []).map((s) => s.name), ...(schedules.data?.rows ?? []).map((r) => r.name)];
	const built = watchSchedule(form, taken);
	const usable = (evaluations.data ?? []).filter((e) => !e.rerun_of && e.spec.arms.length > 0);
	const onSave = async () => {
		if (!built.schedule || !file) return;
		setError("");
		try {
			await save({ file: withWatch(file, built.schedule) }).unwrap();
			setForm(EMPTY_WATCH_FORM);
			onDone();
		} catch (e) {
			setError(errorMessage(e, t("watchForm.saveFailed")));
		}
	};
	return (
		<div className="flex flex-col gap-3 rounded-lg border p-3" data-testid="model-watch-new">
			<WatchFormFields form={form} onChange={setForm} evaluations={usable} />
			{built.problem && <p className="text-muted-foreground text-sm">{t(`watchForm.problems.${built.problem}`)}</p>}
			{error && (
				<p className="text-destructive text-sm" role="alert" data-testid="model-watch-error">
					{error}
				</p>
			)}
			<div className="flex gap-2">
				<Button
					size="sm"
					disabled={!built.schedule || !file || saving.isLoading}
					onClick={() => void onSave()}
					data-testid="model-watch-save"
				>
					{t("watchForm.save")}
				</Button>
				<Button size="sm" variant="ghost" onClick={onDone}>
					{t("watchForm.cancel")}
				</Button>
			</div>
		</div>
	);
}