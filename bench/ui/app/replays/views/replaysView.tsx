import { EmptyNote, LoadError, BlockSkeleton } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetInboxQuery } from "@/lib/store/apis/inboxApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import {
	REPLAY_STATES,
	useGetReplayQuery,
	useGetReplaySettingsQuery,
	useGetReplaysQuery,
	usePutReplaySettingsMutation,
	useReplayToInboxMutation,
	type ReplayReport,
	type ReplaySession,
	type ReplaySettings,
} from "@/lib/store/apis/replaysApi";
import type { InboxSourceStatus } from "@/lib/types/bench";
import { parseAsString, useQueryState } from "nuqs";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import "./w447I18n";

/** A share as a whole percent. */
export function pct(x: number): string {
	return `${Math.round(x * 100)}%`;
}

/** Dollars with up to four decimals. */
export function usd(x: number): string {
	return `$${x.toFixed(x > 0 && x < 0.01 ? 4 : 2)}`;
}

/** The parts of the state bar: every state with steps, in the fixed order, with its share of all steps. */
export function stateBar(counts: Record<string, number> | null | undefined): { state: string; count: number; share: number }[] {
	const c = counts ?? {};
	const total = Object.values(c).reduce((a, b) => a + b, 0);
	if (total === 0) return [];
	return REPLAY_STATES.filter((s) => (c[s] ?? 0) > 0).map((s) => ({ state: s, count: c[s], share: c[s] / total }));
}

/** "provider/model" split for the job params; null when it is not of that form. */
export function splitModel(v: string): { provider: string; model: string } | null {
	const i = v.indexOf("/");
	if (i <= 0 || i === v.length - 1) return null;
	return { provider: v.slice(0, i).trim(), model: v.slice(i + 1).trim() };
}

/** The names of the organization's Bifrost inbox sources, the only sources a replay reads. */
export function bifrostSourceNames(sources: readonly InboxSourceStatus[] | undefined): string[] {
	return (sources ?? []).filter((s) => s.kind === "bifrost").map((s) => s.name);
}

/** The params of replay.run from the form. */
export function replayJobParams(f: ReplayForm): object | null {
	const cand = splitModel(f.candidate);
	if (!cand) return null;
	const judge = f.judge.trim() ? splitModel(f.judge) : undefined;
	if (judge === null) return null;
	const budget = Number(f.cap);
	return {
		source: f.source.trim(),
		since: f.window,
		sessions: Number(f.sessions) || 0,
		only_failed: f.onlyFailed,
		mode: f.mode,
		candidate: { ...cand, budget_usd: budget, yes: f.yes },
		...(judge ? { judge: { ...judge, budget_usd: budget, yes: f.yes } } : {}),
	};
}

export interface ReplayForm {
	source: string;
	window: string;
	sessions: string;
	onlyFailed: boolean;
	mode: "fixed" | "trajectory";
	candidate: string;
	judge: string;
	cap: string;
	yes: boolean;
}

const emptyForm: ReplayForm = {
	source: "",
	window: "7d",
	sessions: "20",
	onlyFailed: false,
	mode: "fixed",
	candidate: "",
	judge: "",
	cap: "2",
	yes: false,
};

/** Shadow replay (/replays?id=): the opt-in, the form, the replays and one replay's report. */
export default function ReplaysView() {
	const { t } = useTranslation("w447");
	const [id, setId] = useQueryState("id", parseAsString.withOptions({ history: "push" }));
	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-6" data-testid="replays-screen">
			<PageHeader title={t("page.title")} subtitle={t("page.intro")} />
			{id ? (
				<ReplayRun id={id} onBack={() => void setId(null)} />
			) : (
				<>
					<SettingsPanel />
					<NewReplay />
					<ReplayList onOpen={(r) => void setId(r)} />
				</>
			)}
		</section>
	);
}

/** The admin toggle with its confirmation. */
export function SettingsToggle({
	settings,
	onSave,
	busy,
	error,
}: {
	settings: ReplaySettings;
	onSave: (s: ReplaySettings) => void;
	busy?: boolean;
	error?: string;
}) {
	const { t } = useTranslation("w447");
	const [asking, setAsking] = useState(false);
	const boxId = useId();
	return (
		<div className="flex flex-col gap-2 rounded-lg border p-4" data-testid="replays-settings">
			<div className="flex items-center gap-2">
				<input
					id={boxId}
					type="checkbox"
					checked={settings.enabled}
					disabled={busy}
					data-testid="replays-toggle"
					onChange={(e) => (e.target.checked ? setAsking(true) : onSave({ ...settings, enabled: false }))}
				/>
				<label htmlFor={boxId} className="text-sm font-medium">
					{t("settings.toggle")}
				</label>
			</div>
			{!settings.enabled && !asking && <p className="text-muted-foreground text-sm">{t("settings.off")}</p>}
			{asking && (
				<div className="flex flex-col gap-2" data-testid="replays-confirm">
					<p className="text-sm">{t("settings.confirm")}</p>
					<div className="flex gap-2">
						<Button
							size="sm"
							onClick={() => {
								setAsking(false);
								onSave({ ...settings, enabled: true });
							}}
						>
							{t("settings.turnOn")}
						</Button>
						<Button size="sm" variant="outline" onClick={() => setAsking(false)}>
							{t("settings.cancel")}
						</Button>
					</div>
				</div>
			)}
			{error && (
				<p className="text-destructive text-sm" role="alert">
					{error}
				</p>
			)}
		</div>
	);
}

function SettingsPanel() {
	const { t } = useTranslation("w447");
	const q = useGetReplaySettingsQuery();
	const [save, saved] = usePutReplaySettingsMutation();
	if (q.isLoading) return <BlockSkeleton rows={1} testId="replays-settings-loading" />;
	if (q.isError || !q.data)
		return <LoadError title={t("settings.saveError")} error={q.error} onRetry={() => void q.refetch()} testId="replays-settings-error" />;
	return (
		<SettingsToggle
			settings={q.data}
			busy={saved.isLoading}
			error={saved.isError ? getBenchErrorMessage(saved.error) || t("settings.saveError") : undefined}
			onSave={(s) => void save(s)}
		/>
	);
}

function Field({ label, hint, children }: { label: string; hint?: string; children: (id: string) => React.ReactNode }) {
	const id = useId();
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{label}
			</label>
			{children(id)}
			{hint && <span className="text-muted-foreground text-xs">{hint}</span>}
		</div>
	);
}

/** The form that starts a replay job. */
export function ReplayFormFields({
	form,
	onChange,
	sources,
}: {
	form: ReplayForm;
	onChange: (f: ReplayForm) => void;
	/** The Bifrost source names of the organization's inbox; undefined while they load. */
	sources?: readonly string[];
}) {
	const { t } = useTranslation("w447");
	const set = <K extends keyof ReplayForm>(k: K, v: ReplayForm[K]) => onChange({ ...form, [k]: v });
	return (
		<div className="grid min-w-0 gap-3 sm:grid-cols-2" data-testid="replays-form">
			<Field label={t("form.source")} hint={sources && sources.length === 0 ? t("form.sourceNone") : t("form.sourceHint")}>
				{(id) => (
					<select
						id={id}
						className="h-9 rounded-md border px-2 text-sm"
						value={form.source}
						onChange={(e) => set("source", e.target.value)}
						disabled={!sources || sources.length === 0}
						data-testid="replays-source"
					>
						<option value="">{t("form.sourcePick")}</option>
						{(sources ?? []).map((name) => (
							<option key={name} value={name}>
								{name}
							</option>
						))}
					</select>
				)}
			</Field>
			<Field label={t("form.window")}>
				{(id) => (
					<select
						id={id}
						className="h-9 rounded-md border px-2 text-sm"
						value={form.window}
						onChange={(e) => set("window", e.target.value)}
					>
						<option value="24h">{t("form.window24h")}</option>
						<option value="7d">{t("form.window7d")}</option>
						<option value="14d">{t("form.window14d")}</option>
					</select>
				)}
			</Field>
			<Field label={t("form.sessions")}>
				{(id) => <Input id={id} type="number" min={1} value={form.sessions} onChange={(e) => set("sessions", e.target.value)} />}
			</Field>
			<Field label={t("form.mode")}>
				{(id) => (
					<select
						id={id}
						className="h-9 rounded-md border px-2 text-sm"
						value={form.mode}
						onChange={(e) => set("mode", e.target.value as ReplayForm["mode"])}
						data-testid="replays-mode"
					>
						<option value="fixed">{t("form.modeFixed")}</option>
						<option value="trajectory">{t("form.modeTrajectory")}</option>
					</select>
				)}
			</Field>
			<Field label={t("form.candidate")} hint={t("form.candidateHint")}>
				{(id) => (
					<Input id={id} value={form.candidate} onChange={(e) => set("candidate", e.target.value)} data-testid="replays-candidate" />
				)}
			</Field>
			<Field label={t("form.judge")} hint={t("form.candidateHint")}>
				{(id) => <Input id={id} value={form.judge} onChange={(e) => set("judge", e.target.value)} />}
			</Field>
			<Field label={t("form.cap")} hint={t("form.capHint")}>
				{(id) => <Input id={id} type="number" min={0} step="0.5" value={form.cap} onChange={(e) => set("cap", e.target.value)} />}
			</Field>
			<label className="flex items-center gap-2 text-sm">
				<input type="checkbox" checked={form.onlyFailed} onChange={(e) => set("onlyFailed", e.target.checked)} />
				{t("form.onlyFailed")}
			</label>
			<label className="flex items-center gap-2 text-sm sm:col-span-2">
				<input type="checkbox" checked={form.yes} onChange={(e) => set("yes", e.target.checked)} data-testid="replays-yes" />
				{t("form.confirmCost")}
			</label>
		</div>
	);
}

function NewReplay() {
	const { t } = useTranslation("w447");
	const [form, setForm] = useState<ReplayForm>(emptyForm);
	const [create, job] = useCreateJobMutation();
	const settings = useGetReplaySettingsQuery();
	const inbox = useGetInboxQuery();
	const sources = inbox.data ? bifrostSourceNames(inbox.data.sources) : undefined;
	const params = replayJobParams(form);
	const enabled = settings.data?.enabled === true;
	return (
		<section className="flex flex-col gap-3 rounded-lg border p-4">
			<h2 className="text-base font-semibold tracking-tight">{t("form.title")}</h2>
			<ReplayFormFields form={form} onChange={setForm} sources={sources} />
			{form.candidate && !params && <p className="text-destructive text-sm">{t("form.badModel")}</p>}
			<div className="flex items-center gap-3">
				<Button
					size="sm"
					disabled={!enabled || !params || !form.yes || !form.source.trim() || job.isLoading}
					onClick={() => params && void create({ kind: "replay.run", params })}
					data-testid="replays-start"
				>
					{t("form.start")}
				</Button>
				{job.isSuccess && <span className="text-sm">{t("form.started")}</span>}
				{job.isError && (
					<span className="text-destructive text-sm" role="alert">
						{getBenchErrorMessage(job.error)}
					</span>
				)}
			</div>
		</section>
	);
}

/** The state bar with the state words. */
export function StateBar({ counts }: { counts: Record<string, number> | null | undefined }) {
	const { t } = useTranslation("w447");
	const parts = stateBar(counts);
	if (parts.length === 0) return null;
	return (
		<ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs" data-testid="replays-state-bar">
			{parts.map((p) => (
				<li key={p.state} data-state={p.state}>
					{t(`state.${p.state}`, { defaultValue: p.state })}: {p.count} ({pct(p.share)})
				</li>
			))}
		</ul>
	);
}

function ReplayList({ onOpen }: { onOpen: (id: string) => void }) {
	const { t } = useTranslation("w447");
	const q = useGetReplaysQuery();
	if (q.isLoading) return <BlockSkeleton rows={3} testId="replays-list-loading" />;
	if (q.isError)
		return <LoadError title={t("list.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="replays-list-error" />;
	const rows = q.data?.replays ?? [];
	return (
		<section className="flex flex-col gap-3">
			<h2 className="text-base font-semibold tracking-tight">{t("list.title")}</h2>
			{rows.length === 0 ? (
				<EmptyNote testId="replays-empty">{t("list.empty")}</EmptyNote>
			) : (
				<ul className="flex flex-col gap-2" data-testid="replays-list">
					{rows.map((r) => (
						<li key={r.id} className="flex flex-col gap-1 rounded-lg border p-3">
							<button
								type="button"
								className="text-left text-sm font-medium underline-offset-2 hover:underline"
								onClick={() => onOpen(r.id)}
							>
								{r.candidate} · {new Date(r.created_at).toLocaleString()}
							</button>
							<span className="text-muted-foreground text-xs">
								{t("list.sessions", { count: r.sessions })} · {t("list.spent", { usd: r.spent_usd.toFixed(2) })}
							</span>
							<StateBar counts={r.counts} />
						</li>
					))}
				</ul>
			)}
		</section>
	);
}

/** The numbers of a run: the equivalent share with its interval, what was left out, the medians. */
export function RunSummary({ report }: { report: ReplayReport }) {
	const { t } = useTranslation("w447");
	const st = report.stats;
	return (
		<div className="flex flex-col gap-2 text-sm" data-testid="replays-summary">
			<p className="text-muted-foreground">{report.note}</p>
			<StateBar counts={report.counts} />
			{st && st.compared > 0 && (
				<p>
					{t("run.equivalentShare", {
						share: pct(st.shares.equivalent_response ?? 0),
						n: st.compared,
						low: pct(st.equivalent_low),
						high: pct(st.equivalent_high),
					})}
				</p>
			)}
			{st && st.left_out > 0 && <p className="text-muted-foreground">{t("run.leftOut", { count: st.left_out })}</p>}
			{st && st.paired > 0 && (
				<p className="text-muted-foreground">
					{t("run.medians", {
						new: usd(st.median_cost_usd),
						recorded: usd(st.median_recorded_cost_usd),
						paired: st.paired,
						newMs: Math.round(st.median_latency_ms),
						recordedMs: Math.round(st.median_recorded_latency_ms),
					})}
				</p>
			)}
			{report.stopped && <p className="text-destructive">{t("run.stopped", { why: report.stopped })}</p>}
		</div>
	);
}

/** One session row: final state, where it stopped, tool agreement, write calls; expands into its steps. */
export function SessionRow({
	session,
	n,
	onInbox,
	sending,
}: {
	session: ReplaySession;
	n: number;
	onInbox?: () => void;
	sending?: boolean;
}) {
	const { t } = useTranslation("w447");
	const [open, setOpen] = useState(false);
	const facts = [
		t(`state.${session.final}`, { defaultValue: session.final }),
		session.diverged_at !== undefined ? t("run.stoppedAt", { n: session.diverged_at + 1 }) : null,
		session.tool_agreement !== undefined ? t("run.toolAgreement", { share: pct(session.tool_agreement) }) : null,
		session.write_set_equal === undefined ? null : session.write_set_equal ? t("run.writeSame") : t("run.writeDifferent"),
	].filter(Boolean);
	return (
		<li className="flex flex-col gap-2 rounded-lg border p-3" data-testid="replays-session">
			<div className="flex flex-wrap items-center gap-3">
				<button type="button" className="text-left text-sm font-medium" aria-expanded={open} onClick={() => setOpen(!open)}>
					{t("run.session", { n })}
				</button>
				<span className="text-muted-foreground text-xs">{facts.join(" · ")}</span>
				{session.final !== "equivalent_response" &&
					(session.inbox_item ? (
						<span className="ml-auto text-xs">{t("run.inInbox")}</span>
					) : onInbox ? (
						<Button size="sm" variant="outline" className="ml-auto" disabled={sending} onClick={onInbox} data-testid="replays-to-inbox">
							{t("run.toInbox")}
						</Button>
					) : null)}
			</div>
			{open && (
				<div className="min-w-0 overflow-x-auto">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>#</TableHead>
								<TableHead>{t("run.recorded")}</TableHead>
								<TableHead>{t("run.new")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{session.steps.map((s, i) => (
								<TableRow key={i}>
									<TableCell className="align-top text-xs tabular-nums">{s.index + 1}</TableCell>
									<TableCell className="align-top text-xs break-words whitespace-pre-wrap">{s.recorded}</TableCell>
									<TableCell className="align-top text-xs break-words whitespace-pre-wrap">
										<div className="font-medium">{t(`state.${s.state}`, { defaultValue: s.state })}</div>
										{s.new}
										{s.judge_note && (
											<div className="text-muted-foreground mt-1">
												{t("run.judgeNote")}: {s.judge_note}
											</div>
										)}
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				</div>
			)}
		</li>
	);
}

function ReplayRun({ id, onBack }: { id: string; onBack: () => void }) {
	const { t } = useTranslation("w447");
	const q = useGetReplayQuery(id);
	const [toInbox, sent] = useReplayToInboxMutation();
	return (
		<div className="flex flex-col gap-4">
			<div>
				<Button size="sm" variant="outline" onClick={onBack}>
					{t("run.back")}
				</Button>
			</div>
			{q.isLoading ? (
				<BlockSkeleton rows={4} testId="replays-run-loading" />
			) : q.isError || !q.data ? (
				<LoadError title={t("run.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="replays-run-error" />
			) : (
				<>
					<h2 className="text-base font-semibold tracking-tight">{q.data.candidate}</h2>
					<RunSummary report={q.data} />
					{sent.isError && (
						<p className="text-destructive text-sm" role="alert">
							{getBenchErrorMessage(sent.error) || t("run.toInboxError")}
						</p>
					)}
					<ul className="flex flex-col gap-2">
						{q.data.sessions.map((s, i) => (
							<SessionRow
								key={s.recording + i}
								session={s}
								n={i + 1}
								sending={sent.isLoading}
								onInbox={() => void toInbox({ id, recording: s.recording })}
							/>
						))}
					</ul>
				</>
			)}
		</div>
	);
}