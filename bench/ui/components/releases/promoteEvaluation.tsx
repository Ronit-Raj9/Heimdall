import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useGetEvaluationReleasePreviewQuery,
	useReleaseEvaluationMutation,
	useScheduleEvaluationMutation,
	type EvaluationCadence,
	type EvaluationReleasePreview,
	type LiveVersion,
} from "@/lib/store/apis/evalReleaseApi";
import { liveReleases, type ReleaseAgent } from "@/components/govern/releaseAgent";
import { useListReleasesQuery } from "@/lib/store/apis/benchApi";
import type { ReleaseRecord } from "@/lib/types/bench";
import type { EvaluationGateResult, GateCheck } from "@/lib/store/apis/evaluationsApi";
import { cn } from "@/lib/utils";
import { browserZone } from "@/lib/utils/cron";
import { PaidButton } from "@/components/ui/paidButton";
import { repeatCost, repeatCostText, type RepeatCost } from "@/app/schedules/views/repeatCost";
import "@/app/schedules/views/ufrjI18n";
import { formatUsd } from "@/lib/money";
import i18n from "@/lib/i18n";
import { InlineError } from "@/components/runs/screenStates";
import { notify } from "@/lib/notify";
import { useGetEvaluationQuery } from "@/lib/store/apis/evaluationsApi";
import "@/components/canaries/uf7bI18n";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, CalendarClock, CheckCircle2, Lock, Rocket, ShieldAlert, ShieldQuestion } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { DatasetChange, ReleaseEvidence, releaseTitle, sharedTaskCount, versionName } from "./releaseEvidence";
import "@/app/experiments_/$expId/views/fw11I18n";

/** The gate's pass or fail as a badge with its summary and each check. Shown on the report card. */
export function GateResultCard({ gate, testId = "evaluation-gate" }: { gate: EvaluationGateResult; testId?: string }) {
	const { t } = useTranslation("u14");
	const pass = gate.state === "pass";
	const fail = gate.state === "fail";
	const Icon = pass ? CheckCircle2 : fail ? ShieldAlert : ShieldQuestion;
	return (
		<section className="bg-card flex flex-col gap-3 rounded-xl border p-5" data-testid={testId} data-state={gate.state}>
			<div className="flex flex-wrap items-center gap-2">
				<Icon
					className={cn("size-4", pass ? "text-chart-success-ink" : fail ? "text-chart-error-ink" : "text-chart-warning-ink")}
					aria-hidden="true"
				/>
				<h2 className="text-sm font-semibold">{t("promote.gateTitle")}</h2>
				<Badge variant={pass ? "success" : fail ? "destructive" : "warning"} data-testid={`${testId}-state`}>
					{t(`promote.gate.${gate.state}` as never, { defaultValue: gate.state })}
				</Badge>
				{gate.baseline && (
					<span className="text-muted-foreground text-xs">
						{t(gate.baseline.live ? "promote.comparedLive" : "promote.compared", {
							name: versionName(gate.baseline.agent, gate.baseline.version),
						})}
					</span>
				)}
			</div>
			<p className="text-sm">{gate.summary}</p>
			{gate.dataset && <DatasetChange dataset={gate.dataset} testId={`${testId}-dataset`} />}
			{gate.checks.length > 0 && (
				<ul className="flex flex-col gap-1 text-xs">
					{gate.checks.map((c) => {
						const detail = gateCheckDetail(c, t as never);
						return (
							<li
								key={`${c.rule}-${c.capability}`}
								className="flex items-start gap-2"
								data-testid={`${testId}-check-${c.rule}-${c.capability}`}
							>
								{c.pass ? (
									<CheckCircle2 className="text-chart-success-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
								) : c.inconclusive ? (
									<ShieldQuestion className="text-chart-warning-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
								) : (
									<AlertTriangle className="text-chart-error-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
								)}
								<span className="flex flex-col gap-0.5">
									<span>{c.summary}</span>
									{detail && <span className="text-muted-foreground tabular-nums">{detail}</span>}
									{(c.notes ?? []).map((n) => (
										<span key={n} className="text-muted-foreground">
											{n}
										</span>
									))}
								</span>
							</li>
						);
					})}
				</ul>
			)}
		</section>
	);
}

/** The paired change and smallest detectable change of a regression check, "" when it has none. */
export function gateCheckDetail(c: GateCheck, t: (k: string, o?: Record<string, unknown>) => string): string {
	const signed = (x: number) => {
		const p = Math.round(x * 100);
		return `${p > 0 ? "+" : ""}${p} pts`;
	};
	const parts: string[] = [];
	if (c.change != null && c.change_low != null && c.change_high != null && c.tasks)
		parts.push(
			t("promote.gateChange", {
				change: signed(c.change),
				tasks: c.tasks,
				low: signed(c.change_low),
				high: signed(c.change_high),
			}),
		);
	if (c.mde != null) parts.push(t("promote.gateMde", { mde: Math.round(c.mde * 100) }));
	return parts.join("; ");
}

/** The headline of a preview: "codex v4 replaces v3" or "codex v4 becomes the first release". */
export function previewTitle(p: EvaluationReleasePreview, t: (k: string, o?: Record<string, unknown>) => string): string {
	return releaseTitle(
		{
			agent_id: p.agent,
			version: p.version,
			replaces_version: p.replaces_version,
			compared_version: p.baseline?.version,
			shared_tasks: sharedTaskCount(p.summary?.dataset),
			agent_config_id: "",
			arm: p.candidate,
		},
		t,
	);
}

/**
 * Whether the preview cannot be released because the evaluated version is already the live one
 * (the agent has one version only, M7-74). Uses `problem_code` when the server sends it, else the
 * version numbers. Exported for tests.
 */
export function onlyLiveVersion(p: Pick<EvaluationReleasePreview, "problem" | "version" | "live"> & { problem_code?: string }): boolean {
	if (!p.problem) return false;
	// G1: same_as_live and only_one_version are one family: the evaluated version is the live one.
	if (p.problem_code) return p.problem_code === "same_as_live" || p.problem_code === "only_one_version";
	return Boolean(p.live.version) && p.live.version === p.version;
}

/**
 * The release log's record to name as live instead of the server's answer (RI, one live rule): only
 * when the server read the live version from a saved config (`source: "saved_config"`) and the log
 * holds a newer release of the same agent, so this page and Releases name the same thing. Pure.
 */
export function recordedLive(
	live: LiveVersion & { source?: string },
	agent: string,
	releases: readonly ReleaseRecord[],
): ReleaseAgent | null {
	if (live.source !== "saved_config") return null;
	const name = live.agent || agent;
	const hit = liveReleases(releases).find((x) => x.agent.agent === name);
	if (!hit) return null;
	if (live.released_at && (hit.release.created_at ?? "") <= live.released_at) return null;
	return hit.agent;
}

/**
 * "Promote this version" on a finished evaluation's report card: what the release would record
 * (agent version, the live version it replaces, before and after numbers, the gate), what blocks it,
 * and the approve form. One release covers every finished benchmark of the evaluation.
 */
export function PromoteEvaluation({ evaluationId }: { evaluationId: string }) {
	const { t } = useTranslation("u14");
	const { t: m } = useTranslation("uf7b");
	const { t: tf } = useTranslation("fw11");
	const session = useSession();
	const preview = useGetEvaluationReleasePreviewQuery(evaluationId);
	const [release, state] = useReleaseEvaluationMutation();
	const [open, setOpen] = useState(false);
	const [approver, setApprover] = useState("");
	const [note, setNote] = useState("");
	const [override, setOverride] = useState(false);
	const [reason, setReason] = useState("");
	const releases = useListReleasesQuery();
	const p = preview.data;
	if (!p || preview.isError) return null;
	const recorded = recordedLive(p.live, p.agent, releases.data?.releases ?? []);
	const title = p.agent ? previewTitle(p, t as never) : t("promote.title");
	const blocked = p.blocked.length > 0;
	const canWrite = canUse(session, "member");
	const enabled =
		!p.problem && (!blocked || (override && reason.trim() !== "")) && !state.isLoading && (approver.trim() !== "" || session.signedIn);
	return (
		<section className="bg-card flex flex-col gap-4 rounded-xl border p-5" data-testid="evaluation-promote">
			<div className="flex flex-wrap items-start justify-between gap-3">
				<div className="flex min-w-0 items-center gap-3">
					<span className="bg-primary/10 text-primary flex size-9 shrink-0 items-center justify-center rounded-lg">
						<Rocket className="size-4.5" aria-hidden="true" />
					</span>
					<div className="min-w-0">
						<h2 className="truncate text-base font-semibold tracking-tight" data-testid="evaluation-promote-title">
							{title}
						</h2>
						<p className="text-muted-foreground text-xs">
							{recorded
								? t("promote.liveNow", { name: versionName(recorded.agent, recorded.version) })
								: p.live.version
								? t("promote.liveNow", { name: versionName(p.agent, p.live.version) })
								: t("promote.nothingLive", { agent: p.agent || "-" })}
						</p>
					</div>
				</div>
				{!state.data && canWrite && !p.problem && (
					<Button size="sm" onClick={() => setOpen((o) => !o)} aria-expanded={open} data-testid="evaluation-promote-open">
						<Rocket className="size-4" aria-hidden="true" />
						{t("promote.button")}
					</Button>
				)}
			</div>
			{p.summary && <ReleaseEvidence summary={p.summary} testId="evaluation-promote-numbers" />}
			{p.problem &&
				(onlyLiveVersion(p) ? (
					<div className="flex flex-col gap-2 rounded-md border px-3 py-2 text-sm" data-testid="evaluation-promote-problem">
						<p data-testid="evaluation-promote-next-step">
							{m("promote.onlyOne", { agent: p.agent, name: versionName(p.agent, p.live.version) })}
						</p>
						<div className="flex flex-wrap gap-2">
							<Button size="sm" asChild>
								<Link
									to="/agents/$agentId"
									params={{ agentId: p.agent }}
									search={{ tab: "versions" } as never}
									data-testid="evaluation-promote-new-version"
								>
									{m("promote.newVersion")}
								</Link>
							</Button>
							<Button size="sm" variant="outline" asChild>
								<Link to="/evaluate" search={{ mode: "versions", agent: p.agent } as never} data-testid="evaluation-promote-evaluate">
									{m("promote.evaluateNew")}
								</Link>
							</Button>
						</div>
					</div>
				) : (
					<p className="text-sm" data-testid="evaluation-promote-problem">
						{p.problem}
					</p>
				))}
			{!p.problem && blocked && (
				<div
					className="border-destructive/40 bg-destructive/5 flex flex-col gap-1 rounded-md border px-3 py-2 text-sm"
					data-testid="evaluation-promote-blocked"
				>
					{p.blocked.map((b) => (
						<p key={b} className="flex items-start gap-2">
							<AlertTriangle className="text-chart-error-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
							{b}
						</p>
					))}
				</div>
			)}
			{!canWrite && !state.data && (
				<p className="text-muted-foreground flex items-center gap-1.5 text-xs" data-testid="evaluation-promote-why">
					<Lock className="size-3 shrink-0" aria-hidden="true" />
					{tf("promote.membersOnly")}
				</p>
			)}
			{open && !state.data && canWrite && (
				<form
					className="flex flex-col gap-3 border-t pt-4"
					data-testid="evaluation-promote-form"
					onSubmit={(e) => {
						e.preventDefault();
						if (!enabled) return;
						void release({
							id: evaluationId,
							...(approver.trim() ? { approver: approver.trim() } : {}),
							...(note.trim() ? { note: note.trim() } : {}),
							...(blocked && override ? { override: reason.trim() } : {}),
						});
					}}
				>
					<div className="grid gap-3 sm:grid-cols-2">
						<label className="text-muted-foreground flex flex-col gap-1 text-xs">
							{t("promote.approver")}
							<Input
								value={approver}
								onChange={(e) => setApprover(e.target.value)}
								placeholder={session.user?.name ?? t("promote.approverPlaceholder")}
								data-testid="evaluation-promote-approver"
							/>
						</label>
						<label className="text-muted-foreground flex flex-col gap-1 text-xs">
							{t("promote.note")}
							<Input value={note} onChange={(e) => setNote(e.target.value)} data-testid="evaluation-promote-note" />
						</label>
					</div>
					{blocked && (
						<div className="flex flex-col gap-2">
							<label className="flex cursor-pointer items-center gap-2 text-sm">
								<input
									type="checkbox"
									className="accent-primary size-4"
									checked={override}
									onChange={(e) => setOverride(e.target.checked)}
									data-testid="evaluation-promote-override"
								/>
								<span className="font-medium">{t("promote.override")}</span>
							</label>
							{override && (
								<Input
									aria-label={t("promote.overrideReason")}
									placeholder={t("promote.overrideReason")}
									value={reason}
									onChange={(e) => setReason(e.target.value)}
									data-testid="evaluation-promote-reason"
								/>
							)}
						</div>
					)}
					<div className="flex items-center gap-3">
						<Button type="submit" disabled={!enabled} isLoading={state.isLoading} dataTestId="evaluation-promote-submit">
							{t("promote.submit", { name: versionName(p.agent, p.version) })}
						</Button>
						<span className="text-muted-foreground text-xs">
							{t("promote.covers", { count: p.children.filter((c) => c.included).length })}
						</span>
					</div>
					{state.isError && (
						<p role="alert" className="text-chart-error-ink text-sm break-words" data-testid="evaluation-promote-error">
							{getBenchErrorMessage(state.error)}
						</p>
					)}
				</form>
			)}
			{state.data && (
				<p className="text-sm" data-testid="evaluation-promote-done">
					{t("promote.done", { name: versionName(p.agent, p.version) })}{" "}
					<Link to="/releases" className="text-primary hover:underline">
						{t("promote.viewReleases")}
					</Link>
				</p>
			)}
		</section>
	);
}

/** Runs a month of each cadence, for the cost line of the confirm. */
const RUNS_A_MONTH: Record<EvaluationCadence, number> = { nightly: 30, weekly: 30 / 7 };

/** The daily limit a repeat run starts with: twice the cost of this run, rounded up to cents. */
export function defaultRepeatLimit(costPerRun: number | undefined): number | undefined {
	if (costPerRun == null || !(costPerRun > 0)) return undefined;
	return Math.ceil(costPerRun * 2 * 100) / 100;
}

/** The typed daily limit as dollars; undefined when empty, NaN when it is not a number above 0. */
export function parseRepeatLimit(text: string): number | undefined {
	const v = text.trim().replace(/^\$/, "");
	if (v === "") return undefined;
	const n = Number(v);
	return Number.isFinite(n) && n > 0 ? Math.round(n * 100) / 100 : Number.NaN;
}

/**
 * The confirm text of "Repeat nightly" and "Repeat weekly" (M7-51): when it runs, in which zone,
 * the cost per run and per month (or that the price is not known), and the daily limit sent.
 * `limit` is the limit the person typed; left out, it is twice the run cost. Exported for tests.
 */
export function repeatAsk(
	cadence: EvaluationCadence,
	zone: string,
	costPerRun: number | undefined,
	t: (k: string, o?: Record<string, unknown>) => string,
	limit: number | undefined = defaultRepeatLimit(costPerRun),
): { message: string; limit?: number; cost: RepeatCost } {
	const when = t(`repeatAsk.when.${cadence}`, { zone });
	const cost = repeatAskCost(cadence, costPerRun, limit);
	const text = repeatCostText(cost);
	const sentence = i18n.t(text.key, { ns: "ufrj", ...text.values });
	return { message: `${when} ${sentence}`, ...(limit != null ? { limit } : {}), cost };
}

/**
 * The confirm's three labelled numbers from the shared repeat helper (lane RJ, R6-3): one run, the daily limit,
 * the month's most. A weekly repeat starts at most one run a week, so its month is 30/7 days at the limit.
 */
export function repeatAskCost(cadence: EvaluationCadence, costPerRun: number | undefined, limit: number | undefined): RepeatCost {
	const c = repeatCost(costPerRun, limit, 1);
	if (cadence === "weekly" && limit != null && limit > 0) return { ...c, monthMax: formatUsd(limit * RUNS_A_MONTH.weekly) };
	return c;
}

/**
 * "Repeat nightly" and "Repeat weekly": schedule a new evaluation of the same spec. Both are paid
 * buttons that always ask first; the daily limit is an editable field beside them (M7-51).
 */
export function RepeatEvaluation({ evaluationId }: { evaluationId: string }) {
	const { t } = useTranslation("u14");
	const { t: m } = useTranslation("uf7b");
	const session = useSession();
	const evaluation = useGetEvaluationQuery(evaluationId);
	const [schedule, state] = useScheduleEvaluationMutation();
	const [done, setDone] = useState<EvaluationCadence | null>(null);
	const [limitText, setLimitText] = useState<string | null>(null);
	const [err, setErr] = useState<unknown>(null);
	const limitId = useId();
	if (!canUse(session, "member")) return null;
	const zone = browserZone();
	const cost = evaluation.data?.result?.cost_usd;
	const fallback = defaultRepeatLimit(cost);
	const typed = limitText ?? (fallback != null ? fallback.toFixed(2) : "");
	const limit = parseRepeatLimit(typed);
	const badLimit = limit != null && Number.isNaN(limit);
	const go = async (cadence: EvaluationCadence) => {
		setErr(null);
		const ask = repeatAsk(cadence, zone, cost, m as never, badLimit ? undefined : limit);
		try {
			await schedule({ id: evaluationId, cadence, timezone: zone, ...(ask.limit ? { max_usd_per_day: ask.limit } : {}) }).unwrap();
			setDone(cadence);
			notify({ text: m("repeatAsk.created") });
		} catch (e) {
			setErr(e);
		}
	};
	return (
		<span className="flex flex-wrap items-start gap-2" data-testid="evaluation-repeat">
			{done ? (
				<span className="text-muted-foreground text-xs" data-testid="evaluation-repeat-done">
					{String(t(`promote.repeatDone.${done}` as never, { zone } as never))}{" "}
					<Link to="/scheduled-checks" search={{ tab: "schedules" } as never} className="text-primary hover:underline">
						{t("promote.viewSchedules")}
					</Link>
				</span>
			) : (
				<>
					<label htmlFor={limitId} className="text-muted-foreground flex flex-col gap-1 text-xs">
						{m("repeatAsk.limitLabel")}
						<Input
							id={limitId}
							inputMode="decimal"
							className="h-8 w-28"
							value={typed}
							placeholder={m("repeatAsk.limitPlaceholder")}
							onChange={(e) => setLimitText(e.target.value)}
							aria-invalid={badLimit || undefined}
							data-testid="evaluation-repeat-limit"
						/>
						{badLimit && (
							<span className="text-danger-ink" data-testid="evaluation-repeat-limit-error">
								{m("repeatAsk.limitBad")}
							</span>
						)}
					</label>
					{(["nightly", "weekly"] as const).map((c) => (
						<PaidButton
							key={c}
							size="sm"
							variant="outline"
							verb={t(`promote.repeat.${c}` as never)}
							capUsd={badLimit ? null : (limit ?? null)}
							// The confirm says the expected cost of one run itself (repeatCost), so no second "About" line.
							confirm="always"
							kind="row"
							confirmText={repeatAsk(c, zone, cost, m as never, badLimit ? undefined : limit).message}
							disabled={state.isLoading || badLimit}
							onConfirm={() => go(c)}
							testId={`evaluation-repeat-${c}`}
						/>
					))}
					<InlineError error={err} testId="evaluation-repeat-error" className="basis-full" />
				</>
			)}
		</span>
	);
}
