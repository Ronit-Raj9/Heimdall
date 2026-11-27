import { LoadError, BlockSkeleton } from "@/components/runs/screenStates";
import { cn } from "@/lib/utils";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { EmptyState } from "@/components/shell/emptyState";
import { PaidButton } from "@/components/ui/paidButton";
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
import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useCancelJobMutation, useGetJobQuery } from "@/lib/store/apis/platformApi";
import "@/components/runs/uf5bI18n";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useId, useState, type ReactNode } from "react";
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

/** Most sessions one replay may take when the org setting does not say. */
export const MAX_REPLAY_SESSIONS = 200;
/** While a replay job is queued or running the page asks for its state and the list this often (M5-59). */
export const REPLAY_POLL_MS = 5_000;

export type ReplayNeed = "needOn" | "needSource" | "needModel" | "needSessions" | "needCap" | "needConsent";

/** What still keeps Start off, in form order; empty when the replay can start (M5-56). */
export function replayProblems(
	f: ReplayForm,
	o: { enabled: boolean; maxSessions?: number; maxUsd?: number },
): ReplayNeed[] {
	const out: ReplayNeed[] = [];
	if (!o.enabled) out.push("needOn");
	if (!f.source.trim()) out.push("needSource");
	if (!replayJobParams(f)) out.push("needModel");
	const n = Number(f.sessions);
	if (!Number.isInteger(n) || n < 1 || n > (o.maxSessions || MAX_REPLAY_SESSIONS)) out.push("needSessions");
	const cap = Number(f.cap);
	if (!(cap > 0) || (o.maxUsd ? cap > o.maxUsd : false)) out.push("needCap");
	if (!f.yes) out.push("needConsent");
	return out;
}

/** "provider/model" from the picker's choice, the form's text value. */
export function modelText(v: { provider: string; model: string }): string {
	return v.provider && v.model ? `${v.provider}/${v.model}` : v.model;
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

/** What a replay still needs before its form can be used (FR-13): the org switch and a session source. Pure. */
export function replaySetupMissing(enabled: boolean | undefined, sources: readonly string[] | undefined): { off: boolean; noSource: boolean } {
	return { off: enabled !== true, noSource: sources !== undefined && sources.length === 0 };
}

/** The first thing on /replays while a replay cannot run: one sentence and the two steps, with their actions. */
export function ReplaySetupSteps({ off, noSource }: { off: boolean; noSource: boolean }) {
	const { t } = useTranslation("w447");
	const step = (done: boolean, text: string, action?: ReactNode) => (
		<li className="flex flex-wrap items-center gap-2">
			<span className={done ? "text-success-ink" : "text-foreground"}>{done ? `${t("setup.done")}:` : "-"}</span>
			<span>{text}</span>
			{!done && action}
		</li>
	);
	return (
		<section className="flex flex-col gap-2 rounded-lg border p-4 text-sm" data-testid="replays-setup">
			<p>{t("setup.what")}</p>
			<ol className="flex flex-col gap-1">
				{step(!off, t("setup.turnOn"))}
				{step(
					!noSource,
					t("setup.connect"),
					<Link to={"/failures" as never} search={{ tab: "connect" } as never} className="text-primary underline-offset-4 hover:underline">
						{t("setup.connectAction")}
					</Link>,
				)}
			</ol>
		</section>
	);
}

/** Shadow replay (/replays?id=): the opt-in, the form, the replays and one replay's report. */
export default function ReplaysView() {
	const { t } = useTranslation("w447");
	const [id, setId] = useQueryState("id", parseAsString.withOptions({ history: "push" }));
	const settings = useGetReplaySettingsQuery();
	const inbox = useGetInboxQuery();
	const missing = replaySetupMissing(settings.data?.enabled, inbox.data ? bifrostSourceNames(inbox.data.sources) : undefined);
	// FR-13: until a replay can run, the setup steps come first and the form stays folded.
	const blocked = settings.data !== undefined && (missing.off || missing.noSource);
	return (
		<section className={cn(pageWidthClass("wide"), "flex flex-col gap-6")} data-page-width="wide" data-testid="replays-screen">
			<PageHeader title={t("page.title")} subtitle={t("page.intro")} />
			{id ? (
				<ReplayRun id={id} onBack={() => void setId(null)} />
			) : (
				<>
					{blocked && <ReplaySetupSteps {...missing} />}
					<SettingsPanel />
					{blocked ? (
						<details className="rounded-lg border" data-testid="replays-form-folded">
							<summary className="text-muted-foreground cursor-pointer px-4 py-3 text-sm font-medium">{t("setup.formFolded")}</summary>
							<div className="p-2">
								<NewReplay />
							</div>
						</details>
					) : (
						<NewReplay />
					)}
					<ReplayList onOpen={(r) => void setId(r)} />
				</>
			)}
		</section>
	);
}

/**
 * The org switch (M5-54): a status line "Shadow replay: Off" and, for admins, a switch that asks
 * before turning on. Everyone else reads who can turn it on.
 */
export function SettingsToggle({
	settings,
	onSave,
	busy,
	error,
	admin = true,
}: {
	settings: ReplaySettings;
	onSave: (s: ReplaySettings) => void;
	busy?: boolean;
	error?: string;
	/** The viewer may change the setting (an org admin). */
	admin?: boolean;
}) {
	const { t } = useTranslation("w447");
	const { t: t5 } = useTranslation("uf5b");
	const [asking, setAsking] = useState(false);
	const on = settings.enabled;
	return (
		<div className="flex flex-col gap-2 rounded-lg border p-4" data-testid="replays-settings">
			<div className="flex flex-wrap items-center gap-3">
				{admin && (
					<button
						type="button"
						role="switch"
						aria-checked={on}
						aria-label={t("settings.toggle")}
						disabled={busy}
						data-testid="replays-toggle"
						onClick={() => (on ? onSave({ ...settings, enabled: false }) : setAsking(true))}
						className={
							"focus-visible:ring-ring relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full transition-colors focus-visible:ring-2 focus-visible:outline-none disabled:opacity-50 " +
							(on ? "bg-primary" : "bg-muted-foreground/30")
						}
					>
						<span
							aria-hidden="true"
							className={"bg-background inline-block size-5 rounded-full shadow transition-transform " + (on ? "translate-x-5" : "translate-x-0.5")}
						/>
					</button>
				)}
				<span className="text-sm font-medium" data-testid="replays-status">
					{on ? t5("replays.statusOn") : t5("replays.statusOff")}
				</span>
				{admin && !on && !asking && (
					<Button size="sm" variant="outline" disabled={busy} onClick={() => setAsking(true)} data-testid="replays-turn-on">
						{t5("replays.turnOn")}
					</Button>
				)}
			</div>
			{!on && !asking && !admin && <p className="text-muted-foreground text-sm">{t("settings.off")}</p>}
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
	const admin = canUse(useSession(), "admin");
	if (q.isLoading) return <BlockSkeleton rows={1} testId="replays-settings-loading" />;
	if (q.isError || !q.data)
		return <LoadError title={t("settings.saveError")} error={q.error} onRetry={() => void q.refetch()} testId="replays-settings-error" />;
	return (
		<SettingsToggle
			settings={q.data}
			busy={saved.isLoading}
			error={saved.isError ? getBenchErrorMessage(saved.error) || t("settings.saveError") : undefined}
			onSave={(s) => void save(s)}
			admin={admin}
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

/** The form that starts a replay job; disabled as a whole while shadow replay is off (M5-54). */
export function ReplayFormFields({
	form,
	onChange,
	sources,
	disabled,
}: {
	form: ReplayForm;
	onChange: (f: ReplayForm) => void;
	/** The Bifrost source names of the organization's inbox; undefined while they load. */
	sources?: readonly string[];
	disabled?: boolean;
}) {
	const { t } = useTranslation("w447");
	const { t: t5 } = useTranslation("uf5b");
	const set = <K extends keyof ReplayForm>(k: K, v: ReplayForm[K]) => onChange({ ...form, [k]: v });
	const picked = (v: string) => splitModel(v) ?? (v ? { provider: "", model: v } : null);
	return (
		<fieldset disabled={disabled} className="grid min-w-0 gap-3 disabled:opacity-60 sm:grid-cols-2" data-testid="replays-form">
			<div className="flex min-w-0 flex-col gap-2">
				<Field label={t("form.source")} hint={sources && sources.length === 0 ? undefined : t("form.sourceHint")}>
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
				{sources && sources.length === 0 && (
					// No source yet: say where one comes from, with the way there (M5-55).
					<p className="text-muted-foreground text-xs" data-testid="replays-no-sources">
						{t5("replays.noSources")}:{" "}
						<Link to={"/failures" as never} search={{ tab: "connect" } as never} className="text-primary underline-offset-4 hover:underline">
							{t5("replays.connect")}
						</Link>
					</p>
				)}
				{/* The session filter sits with the source it filters (M5-58). */}
				<label className="flex items-center gap-2 text-sm">
					<input type="checkbox" className="size-4" checked={form.onlyFailed} onChange={(e) => set("onlyFailed", e.target.checked)} />
					{t("form.onlyFailed")}
				</label>
			</div>
			<Field label={t("form.window")}>
				{(id) => (
					<select id={id} className="h-9 rounded-md border px-2 text-sm" value={form.window} onChange={(e) => set("window", e.target.value)}>
						<option value="24h">{t("form.window24h")}</option>
						<option value="7d">{t("form.window7d")}</option>
						<option value="14d">{t("form.window14d")}</option>
					</select>
				)}
			</Field>
			<Field label={t("form.sessions")}>
				{(id) => (
					<Input
						id={id}
						type="number"
						min={1}
						max={MAX_REPLAY_SESSIONS}
						step={1}
						size="number"
						value={form.sessions}
						onChange={(e) => set("sessions", e.target.value)}
						data-testid="replays-sessions"
					/>
				)}
			</Field>
			<Field label={t("form.mode")} hint={form.mode === "fixed" ? t("form.modeFixedHelp") : t("form.modeTrajectoryHelp")}>
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
			{/* Model fields are the shared model picker, not free text (M5-57). */}
			<div className="flex min-w-0 flex-col gap-1">
				<span className="text-muted-foreground text-xs">{t("form.candidate")}</span>
				<GroupedModelPicker
					value={picked(form.candidate)}
					onChange={(v) => set("candidate", modelText(v))}
					label={t("form.candidate")}
					disabled={disabled}
					testId="replays-candidate"
				/>
			</div>
			<div className="flex min-w-0 flex-col gap-1">
				<span className="text-muted-foreground text-xs">{t("form.judge")}</span>
				<GroupedModelPicker
					value={picked(form.judge)}
					onChange={(v) => set("judge", modelText(v))}
					label={t("form.judge")}
					disabled={disabled}
					testId="replays-judge"
				/>
				{form.judge && (
					<button type="button" className="text-primary self-start text-xs underline-offset-4 hover:underline" onClick={() => set("judge", "")}>
						{t("settings.cancel")}
					</button>
				)}
			</div>
			<Field label={t("form.cap")} hint={t("form.capHint")}>
				{(id) => (
					<span className="relative inline-flex w-24 items-center">
						<span aria-hidden="true" className="text-muted-foreground pointer-events-none absolute left-2 text-sm">
							$
						</span>
						<Input
							id={id}
							type="number"
							min={0}
							step="0.5"
							size="number"
							className="pl-5"
							value={form.cap}
							onChange={(e) => set("cap", e.target.value)}
							data-testid="replays-cap"
						/>
					</span>
				)}
			</Field>
			<label className="flex items-center gap-2 text-sm sm:col-span-2">
				<input type="checkbox" className="size-4" checked={form.yes} onChange={(e) => set("yes", e.target.checked)} data-testid="replays-yes" />
				{t("form.confirmCost")}
			</label>
		</fieldset>
	);
}

function NewReplay() {
	const { t } = useTranslation("w447");
	const { t: t5 } = useTranslation("uf5b");
	const [form, setForm] = useState<ReplayForm>(emptyForm);
	const [create, job] = useCreateJobMutation();
	const settings = useGetReplaySettingsQuery();
	const inbox = useGetInboxQuery();
	const sources = inbox.data ? bifrostSourceNames(inbox.data.sources) : undefined;
	const params = replayJobParams(form);
	const enabled = settings.data?.enabled === true;
	const problems = replayProblems(form, {
		enabled,
		maxSessions: settings.data?.max_sessions_per_run,
		maxUsd: settings.data?.max_usd_per_run,
	});
	// After Start the job is followed until it ends; the list below refreshes with it (M5-59).
	const jobId = job.data?.id;
	// Both polls stop once the job has ended (M1-62).
	const [jobEnded, setJobEnded] = useState(false);
	const jobState = useGetJobQuery(jobId ?? skipToken, { pollingInterval: jobEnded ? 0 : REPLAY_POLL_MS, skipPollingIfUnfocused: true });
	const active = Boolean(jobId) && (!jobState.data || jobState.data.status === "queued" || jobState.data.status === "running");
	const ended = Boolean(jobId) && !active;
	useEffect(() => setJobEnded(ended), [ended]);
	useGetReplaysQuery(undefined, { pollingInterval: active ? REPLAY_POLL_MS : 0, skipPollingIfUnfocused: true });
	const [cancel, cancelled] = useCancelJobMutation();
	const why = problems.map((k) =>
		k === "needSessions" ? t5("replays.needSessions", { max: settings.data?.max_sessions_per_run || MAX_REPLAY_SESSIONS }) : t5(`replays.${k}`),
	);
	return (
		<section className="flex flex-col gap-3 rounded-lg border p-4">
			<h2 className="text-base font-semibold tracking-tight">{t("form.title")}</h2>
			<ReplayFormFields form={form} onChange={setForm} sources={sources} disabled={!enabled} />
			{form.candidate && !params && <p className="text-destructive text-sm">{t("form.badModel")}</p>}
			{Number(form.cap) > 0 && Number(form.sessions) > 0 && (
				<p className="text-muted-foreground text-xs" data-testid="replays-cap-estimate">
					{t5("replays.capEstimate", { sessions: Number(form.sessions), cap: usd(Number(form.cap)) })}
				</p>
			)}
			<div className="flex flex-wrap items-center gap-3">
				{/* Start spends money: the shared PaidButton names the cap and asks first (M1-51). */}
				<PaidButton
					size="sm"
					verb={t("form.start")}
					capUsd={Number(form.cap) > 0 ? Number(form.cap) : null}
					estimateRuns={Number(form.sessions) > 0 ? Number(form.sessions) : undefined}
					estimateAt="title"
					confirm="always"
					kind="form"
					disabled={problems.length > 0 || active}
					isLoading={job.isLoading}
					aria-describedby={problems.length > 0 ? "replays-why" : undefined}
					onConfirm={async () => {
						if (params) await create({ kind: "replay.run", params });
					}}
					testId="replays-start"
				/>
				{problems.length > 0 && (
					<span id="replays-why" className="text-muted-foreground text-xs" data-testid="replays-why">
						{t5("replays.why", { list: why.join(", ") })}
					</span>
				)}
				{job.isSuccess && <span className="text-sm">{t("form.started")}</span>}
				{active && jobId && (
					// Stop cancels the replay job (M1-53).
					<Button size="sm" variant="outline" disabled={cancelled.isLoading} onClick={() => void cancel(jobId)} data-testid="replays-stop">
						{t5("replays.stop")}
					</Button>
				)}
				{cancelled.isSuccess && <span className="text-sm">{t5("replays.stopped")}</span>}
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
				<EmptyState compact body={t("list.empty")} testId="replays-empty" />
			) : (
				<ul className="flex flex-col gap-2" data-testid="replays-list">
					{rows.map((r) => (
						<li key={r.id} className="flex flex-col gap-1 rounded-lg border p-3">
							{/* A real link to ?id=, so a replay opens in a new tab and its address can be copied (X-87, M7-32). */}
							<Link
								to={"/replays" as never}
								search={{ id: r.id } as never}
								className="self-start text-left text-sm font-medium underline-offset-2 hover:underline"
								onClick={(e) => {
									if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
									e.preventDefault();
									onOpen(r.id);
								}}
								data-testid={`replays-open-${r.id}`}
							>
								{r.candidate} · {new Date(r.created_at).toLocaleString()}
							</Link>
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