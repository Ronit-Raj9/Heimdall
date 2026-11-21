import { PageHeader } from "@/components/shell";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
	errorMessage,
	useGetSchedulesQuery,
	useRunScheduleMutation,
	useSaveSchedulesMutation,
	useTestAlertMutation,
} from "@/lib/store/apis/schedulesApi";
import type { AlertConfig, Schedule, ScheduleRow, SchedulesFile, SchedulesResponse } from "@/lib/types/schedules";
import { useState } from "react";
import { useTranslation } from "react-i18next";

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

	const persist = async (body: { file?: SchedulesFile; yaml?: string }) => {
		setMsg(null);
		const res = await save(body);
		if ("error" in res && res.error) setMsg(t("saveError", { error: errorMessage(res.error, "unknown error") }));
		else setMsg(t("saved"));
	};

	return (
		<section className="flex min-w-0 flex-col gap-6 p-6" data-testid="schedules-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				meta={
					<Button size="sm" variant="outline" onClick={() => setYamlMode((v) => !v)} data-testid="schedules-yaml-toggle">
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
				<SchedulesBody data={q.data} busy={saveState.isLoading} onSave={(file) => void persist({ file })} />
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
	onSave(file: SchedulesFile): void;
}

/** The loaded schedules, the add form and the webhook alerts. Exported for tests. */
export function SchedulesBody({ data, busy, onSave }: SchedulesBodyProps) {
	const { t } = useTranslation("schedules");
	const file = data.file;
	const saveWith = (patch: Partial<SchedulesFile>) => onSave({ alerts: file.alerts ?? [], schedules: file.schedules ?? [], ...patch });
	return (
		<div className="flex min-w-0 flex-col gap-6">
			{!data.ticker && (
				<p className="text-muted-foreground text-xs" data-testid="schedules-no-ticker">
					{t("noTicker")}
				</p>
			)}
			<Section title={t("title")} testId="schedules-list">
				{data.rows.length === 0 ? (
					<EmptyNote testId="schedules-empty">{t("empty")}</EmptyNote>
				) : (
					<ScheduleTable
						rows={data.rows}
						onRemove={(name) => saveWith({ schedules: file.schedules.filter((s) => s.name !== name) })}
						busy={busy}
					/>
				)}
			</Section>
			<Section title={t("add.title")} testId="schedules-add">
				<AddScheduleForm busy={busy} onAdd={(s) => saveWith({ schedules: [...(file.schedules ?? []), s] })} />
			</Section>
			<Section title={t("alerts.title")} description={t("alerts.description")} testId="schedules-alerts">
				<AlertsEditor
					alerts={file.alerts ?? []}
					events={data.events}
					secrets={data.secrets}
					busy={busy}
					onChange={(alerts) => saveWith({ alerts })}
				/>
			</Section>
		</div>
	);
}

function ScheduleTable({ rows, onRemove, busy }: { rows: ScheduleRow[]; onRemove(name: string): void; busy: boolean }) {
	const { t } = useTranslation("schedules");
	const [run, runState] = useRunScheduleMutation();
	const [note, setNote] = useState<string | null>(null);
	const runNow = async (name: string) => {
		setNote(null);
		const res = await run(name);
		if ("data" in res && res.data) setNote(t("queued", { id: res.data.job_id }));
		else if ("error" in res) setNote(errorMessage(res.error, "not queued"));
	};
	return (
		<div className="flex flex-col gap-2">
			<table className="w-full text-sm" data-testid="schedules-table">
				<thead className="text-muted-foreground text-left text-xs">
					<tr>
						<th>{t("table.name")}</th>
						<th>{t("table.kind")}</th>
						<th>{t("table.cron")}</th>
						<th>{t("table.next")}</th>
						<th>{t("table.last")}</th>
						<th>{t("table.spent")}</th>
						<th>{t("table.actions")}</th>
					</tr>
				</thead>
				<tbody>
					{rows.map((r) => (
						<tr key={r.name} className="border-t align-top" data-testid={`schedule-row-${r.name}`}>
							<td className="py-2 font-medium">
								{r.name}
								{r.problem && <div className="text-chart-error-ink text-xs">{r.problem}</div>}
							</td>
							<td className="py-2">
								{r.kind}
								<div className="text-muted-foreground text-xs">{r.job || r.experiment}</div>
							</td>
							<td className="py-2 font-mono text-xs">
								{r.cron}
								{r.timezone ? ` ${r.timezone}` : ""}
							</td>
							<td className="py-2 text-xs">{r.next ? new Date(r.next).toLocaleString() : "-"}</td>
							<td className="py-2 text-xs">
								{r.last ? `${r.last.status}${r.last.reason ? `: ${r.last.reason}` : ""}` : t("never")}
							</td>
							<td className="py-2 text-xs">
								{r.spent_today_usd.toFixed(2)}
								{r.max_usd_per_day ? ` / ${r.max_usd_per_day.toFixed(2)}` : ""}
							</td>
							<td className="flex gap-2 py-2">
								<Button size="sm" variant="outline" disabled={runState.isLoading} onClick={() => void runNow(r.name)} data-testid={`schedule-run-${r.name}`}>
									{t("runNow")}
								</Button>
								{r.source === "web" ? (
									<Button size="sm" variant="ghost" disabled={busy} onClick={() => onRemove(r.name)} data-testid={`schedule-remove-${r.name}`}>
										{t("remove")}
									</Button>
								) : (
									<span className="text-muted-foreground self-center text-xs">{t("fromCanary")}</span>
								)}
							</td>
						</tr>
					))}
				</tbody>
			</table>
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

function AddScheduleForm({ busy, onAdd }: { busy: boolean; onAdd(s: Schedule): void }) {
	const { t } = useTranslation("schedules");
	const [s, setS] = useState({ name: "", experiment: "", cron: "0 6 * * *", timezone: "", max: "" });
	const set = (k: keyof typeof s) => (e: React.ChangeEvent<HTMLInputElement>) => setS({ ...s, [k]: e.target.value });
	const ok = s.name && s.experiment && s.cron;
	return (
		<form
			className="grid gap-3 md:grid-cols-5"
			onSubmit={(e) => {
				e.preventDefault();
				if (!ok) return;
				const max = Number(s.max);
				onAdd({
					name: s.name.trim(),
					experiment: s.experiment.trim(),
					cron: s.cron.trim(),
					...(s.timezone ? { timezone: s.timezone.trim() } : {}),
					...(max > 0 ? { max_usd_per_day: max } : {}),
				});
			}}
			data-testid="schedule-add-form"
		>
			<Field label={t("add.name")}>
				<Input value={s.name} onChange={set("name")} placeholder={t("add.namePlaceholder")} data-testid="schedule-add-name" />
			</Field>
			<Field label={t("add.experiment")}>
				<Input value={s.experiment} onChange={set("experiment")} placeholder={t("add.experimentPlaceholder")} data-testid="schedule-add-experiment" />
			</Field>
			<Field label={t("add.cron")}>
				<Input value={s.cron} onChange={set("cron")} className="font-mono" data-testid="schedule-add-cron" />
			</Field>
			<Field label={t("add.timezone")}>
				<Input value={s.timezone} onChange={set("timezone")} placeholder={t("add.timezonePlaceholder")} />
			</Field>
			<Field label={t("add.maxUsd")}>
				<Input value={s.max} onChange={set("max")} inputMode="decimal" data-testid="schedule-add-max" />
			</Field>
			<p className="text-muted-foreground text-xs md:col-span-4">{t("add.hint")}</p>
			<div>
				<Button type="submit" size="sm" disabled={busy || !ok} data-testid="schedule-add-submit">
					{t("add.submit")}
				</Button>
			</div>
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
							<Button size="sm" variant="outline" disabled={testState.isLoading} onClick={() => void sendTest(a)} data-testid={`alert-test-${i}`}>
								{t("alerts.test")}
							</Button>
							<Button size="sm" variant="ghost" disabled={busy} onClick={() => onChange(alerts.filter((_, j) => j !== i))}>
								{t("remove")}
							</Button>
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
					<Input list="schedule-secret-names" value={draft.secret_env ?? ""} onChange={(e) => setDraft({ ...draft, secret_env: e.target.value || undefined })} />
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
				<Button size="sm" variant="outline" disabled={testState.isLoading || !draft.url_env} onClick={() => void sendTest(draft)} data-testid="alert-test-draft">
					{t("alerts.test")}
				</Button>
				{draft.url_env && results[draft.url_env] && <span className="self-center text-xs">{results[draft.url_env]}</span>}
			</div>
		</div>
	);
}
