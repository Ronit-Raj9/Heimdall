import { RoleGate } from "@/components/platform/accessNotes";
import { ConfirmButton } from "@/components/platform/badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { Input } from "@/components/ui/input";
import { formatCost } from "@/components/leaderboard/agentBoard.utils";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	effectiveCap,
	effectiveConcurrency,
	requeueable,
	useCancelRunMutation,
	useGetRunControlsQuery,
	usePatchRunControlsMutation,
	usePauseEvaluationMutation,
	usePauseExperimentMutation,
	useRequeueRunMutation,
	useRerunEvaluationFailuresMutation,
	useRerunFailuresMutation,
	useResumeEvaluationMutation,
	useUnpauseExperimentMutation,
	type ControlLogEntry,
	type ErrorPolicyRow,
	type RunControls,
	type RunControlsPatch,
} from "@/lib/store/apis/runControlApi";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/runControl";
import "@/app/compare/views/w4m3I18n";
import { Link } from "@tanstack/react-router";
import { ArrowRight, Pause, Play, RotateCcw, ShieldCheck, ShieldAlert } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

/** GET /controls with the display names the hosted server adds (name, else email); ids stay when absent. */
type NamedRunControls = RunControls & { paused_by_name?: string; actor_names?: Record<string, string> };

/** An evaluation with the failed run count the server adds (fix wave 3, N183); absent on an older server. */
type EvaluationWithFailures = Evaluation & { failed_runs?: number };

/**
 * Whether an evaluation has a failed run to rerun: the server's failed_runs, else (an evaluation stored before
 * the field existed) a stored result whose score is below 100% on some arm.
 */
export function hasFailedRuns(evaluation: Evaluation): boolean {
	const n = (evaluation as EvaluationWithFailures).failed_runs;
	if (typeof n === "number") return n > 0;
	return (evaluation.result?.arms ?? []).some((a) => a.runs > 0 && a.score < 1);
}

type ControlSettings = { mode?: string; concurrency?: number; total_usd?: number; paused_models?: string[] };

function settingsOf(v: unknown): ControlSettings {
	return v && typeof v === "object" ? (v as ControlSettings) : {};
}

/**
 * A "set" change log entry in plain words (fix wave 3, N199): "held gpt-5.4-mini", "released ...",
 * "set runs at once from 2 to 4", "set the spend cap from $1.00 to $2.00", "paused" or "resumed", joined
 * with "; ". Anything else reads "changed settings".
 */
export function describeControlChange(e: Pick<ControlLogEntry, "before" | "after">, t: TFunction<"runControl">): string {
	const b = settingsOf(e.before);
	const a = settingsOf(e.after);
	const out: string[] = [];
	const before = b.paused_models ?? [];
	const after = a.paused_models ?? [];
	const held = after.filter((m) => !before.includes(m));
	const released = before.filter((m) => !after.includes(m));
	if (held.length > 0) out.push(t("log.held", { models: held.join(", ") }));
	if (released.length > 0) out.push(t("log.released", { models: released.join(", ") }));
	if ((b.concurrency ?? 0) !== (a.concurrency ?? 0)) {
		const n = (v?: number) => (v ? String(v) : t("log.frozen"));
		out.push(t("log.concurrency", { a: n(b.concurrency), b: n(a.concurrency) }));
	}
	if ((b.total_usd ?? 0) !== (a.total_usd ?? 0)) {
		const usd = (v?: number) => (v ? formatCost(v) : t("log.frozen"));
		out.push(t("log.spendCap", { a: usd(b.total_usd), b: usd(a.total_usd) }));
	}
	if (b.mode && a.mode && b.mode !== a.mode) out.push(t(a.mode === "paused" ? "log.actions.pause" : "log.actions.resume"));
	return out.length > 0 ? out.join("; ") : t("log.actions.set");
}

/** The display text of a run control actor: the name the server sent, else the id. */
export function actorName(c: RunControls, id: string): string {
	return (c as NamedRunControls).actor_names?.[id] || id;
}

/** Who paused: the name the server sent, else the id. */
export function pausedByName(c: RunControls): string {
	return (c as NamedRunControls).paused_by_name || c.state.paused_by || "";
}

function ErrorLine({ error, title, testId }: { error: unknown; title: string; testId: string }) {
	return (
		<p role="alert" className="text-chart-error-ink w-full text-xs" data-testid={testId}>
			{title}: {getBenchErrorMessage(error)}
		</p>
	);
}

/**
 * Pause and resume beside Cancel on a running experiment. Pause drains: runs already going finish
 * and nothing new starts. While paused, a chip says how many runs are still going.
 */
export function PauseControls({ expId }: { expId: string }) {
	const { t } = useTranslation("runControl");
	const controls = useGetRunControlsQuery(expId, { pollingInterval: 5000 });
	const [pause, pauseState] = usePauseExperimentMutation();
	const [resume, resumeState] = useUnpauseExperimentMutation();
	const c = controls.data;
	if (!c) return null;
	return (
		<>
			{c.paused ? (
				<>
					<Badge
						variant="outline"
						className="gap-1"
						data-testid="experiment-paused-chip"
						title={c.state.paused_by ? t("pausedBy", { who: pausedByName(c) }) : undefined}
					>
						<Pause className="size-3" aria-hidden="true" />
						{c.in_flight > 0 ? t("draining", { count: c.in_flight }) : t("pausedBadge")}
					</Badge>
					<Button
						variant="outline"
						size="sm"
						className="h-7 gap-1.5 px-2.5 text-xs"
						isLoading={resumeState.isLoading}
						onClick={() => void resume({ expId })}
						dataTestId="experiment-unpause"
					>
						<Play className="size-3.5" aria-hidden="true" />
						{t("resume")}
					</Button>
				</>
			) : (
				<Button
					variant="outline"
					size="sm"
					className="h-7 gap-1.5 px-2.5 text-xs"
					title={t("pauseHint")}
					isLoading={pauseState.isLoading}
					onClick={() => void pause({ expId })}
					dataTestId="experiment-pause"
				>
					<Pause className="size-3.5" aria-hidden="true" />
					{t("pause")}
				</Button>
			)}
			{pauseState.error && <ErrorLine error={pauseState.error} title={t("pauseError")} testId="experiment-pause-error" />}
			{resumeState.error && <ErrorLine error={resumeState.error} title={t("resumeError")} testId="experiment-unpause-error" />}
		</>
	);
}

function PolicyTable({ rows }: { rows: ErrorPolicyRow[] }) {
	const { t } = useTranslation("runControl");
	return (
		<div className="min-w-0 overflow-x-auto">
			<table className="w-full text-left text-xs" data-testid="error-policy-table">
				<caption className="text-foreground mb-2 text-left text-sm font-medium">{t("policy.title")}</caption>
				<thead className="text-muted-foreground">
					<tr>
						<th className="py-1 pr-3 font-medium">{t("policy.class")}</th>
						<th className="py-1 pr-3 font-medium">{t("policy.retry")}</th>
						<th className="py-1 pr-3 font-medium">{t("policy.max")}</th>
						<th className="py-1 pr-3 font-medium">{t("policy.counts")}</th>
						<th className="py-1 font-medium">{t("policy.grade")}</th>
					</tr>
				</thead>
				<tbody>
					{rows.map((r) => (
						<tr key={r.class} className="border-t" title={r.note} data-testid={`policy-row-${r.class}`}>
							<td className="py-1.5 pr-3">{t(`policy.classes.${r.class}`, { defaultValue: r.class })}</td>
							<td className="py-1.5 pr-3">{t(`policy.retries.${r.retry}`, { defaultValue: r.retry })}</td>
							<td className="py-1.5 pr-3 tabular-nums">{r.max_attempts > 0 ? r.max_attempts : "-"}</td>
							<td className="py-1.5 pr-3">{r.counts ? t("policy.yes") : t("policy.no")}</td>
							<td className="py-1.5">{t(`policy.grades.${r.grade}`, { defaultValue: r.grade })}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

function ControlLog({ c }: { c: RunControls }) {
	const { t } = useTranslation("runControl");
	const { t: tw } = useTranslation("w4m3");
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid="control-log">
			<p className="flex items-center gap-1.5 text-sm font-medium">
				{c.log_verified ? (
					<ShieldCheck className="text-muted-foreground size-4" aria-hidden="true" />
				) : (
					<ShieldAlert className="text-chart-error-ink size-4" aria-hidden="true" />
				)}
				{t("log.title")}
				<span className="text-muted-foreground font-normal">
					{c.log_verified ? tw("controls.verified", { count: c.log_entries }) : t("log.broken", { problem: c.log_problem ?? "" })}
				</span>
			</p>
			{c.log.length === 0 ? (
				<p className="text-muted-foreground text-xs">{t("log.empty")}</p>
			) : (
				<ol className="flex flex-col gap-1 text-xs">
					{[...c.log].reverse().map((e: ControlLogEntry) => (
						<li key={e.seq} className="flex flex-wrap gap-x-2" data-testid="control-log-entry">
							<time className="text-muted-foreground tabular-nums" dateTime={e.at}>
								{new Date(e.at).toLocaleString()}
							</time>
							<span className="font-medium" title={e.actor} data-testid="control-log-actor">
								{actorName(c, e.actor)}
							</span>
							<span data-testid="control-log-action">
								{e.action === "set" ? describeControlChange(e, t) : t(`log.actions.${e.action}`, { defaultValue: e.action })}
							</span>
							{e.target && <span className="text-muted-foreground font-mono">{e.target}</span>}
							<span className="text-muted-foreground">{e.reason || t("log.noReason")}</span>
						</li>
					))}
				</ol>
			)}
		</div>
	);
}

/** Parses a number field; empty means "leave as is" (null). */
function numberOrNull(s: string): number | null {
	if (s.trim() === "") return null;
	const n = Number(s);
	return Number.isFinite(n) && n >= 0 ? n : NaN;
}

/**
 * The patch the live settings form sends (p05-3): a box that repeats the value in effect sends
 * nothing, so the log never records a change that is not one. dirty is true only when the
 * concurrency, the spend cap or the held models change; invalid when a box holds a bad number.
 */
export function controlsPatch(
	c: RunControls,
	conc: string,
	cap: string,
	held: string[],
	reason: string,
): { patch: RunControlsPatch; dirty: boolean; invalid: boolean } {
	const concN = numberOrNull(conc);
	const capN = numberOrNull(cap);
	const invalid = Number.isNaN(concN) || Number.isNaN(capN) || (concN !== null && (!Number.isInteger(concN) || concN > 64));
	const concChanged = concN !== null && !Number.isNaN(concN) && concN !== effectiveConcurrency(c);
	const capChanged = capN !== null && !Number.isNaN(capN) && capN !== effectiveCap(c);
	const heldChanged = held.join(",") !== (c.state.paused_models ?? []).join(",");
	const patch: RunControlsPatch = {
		...(concChanged && concN !== null ? { concurrency: concN } : {}),
		...(capChanged && capN !== null ? { total_usd: capN } : {}),
		...(heldChanged ? { paused_models: held } : {}),
		...(reason.trim() ? { reason: reason.trim() } : {}),
	};
	return { patch, dirty: concChanged || capChanged || heldChanged, invalid };
}

/** The live settings form: runs at once, spend cap, held models, reason. Members and up. */
function LiveSettings({ c, live }: { c: RunControls; live: boolean }) {
	const { t } = useTranslation("runControl");
	const [patch, state] = usePatchRunControlsMutation();
	const [conc, setConc] = useState("");
	const [cap, setCap] = useState("");
	const [held, setHeld] = useState<string[]>(c.state.paused_models ?? []);
	const [reason, setReason] = useState("");
	useEffect(() => setHeld(c.state.paused_models ?? []), [c.state.paused_models]);
	const { patch: body, dirty, invalid } = controlsPatch(c, conc, cap, held, reason);
	const submit = () => {
		void patch({
			expId: c.exp_id,
			patch: body,
		})
			.unwrap()
			.then(() => {
				setConc("");
				setCap("");
				setReason("");
			})
			.catch(() => undefined);
	};
	return (
		<RoleGate min="member" fallback={null}>
			<form
				className="grid min-w-0 gap-3 sm:grid-cols-2"
				onSubmit={(e) => {
					e.preventDefault();
					submit();
				}}
				data-testid="live-settings"
			>
				<label className="flex flex-col gap-1 text-xs">
					<span className="font-medium">
						{t("panel.concurrency")}: <span className="tabular-nums">{effectiveConcurrency(c)}</span>
					</span>
					<Input
						inputMode="numeric"
						placeholder={String(effectiveConcurrency(c))}
						value={conc}
						onChange={(e) => setConc(e.target.value)}
						disabled={!live}
						data-testid="live-concurrency"
					/>
					<span className="text-muted-foreground">{t("panel.concurrencyHint", { value: c.concurrency })}</span>
				</label>
				<label className="flex flex-col gap-1 text-xs">
					<span className="font-medium">
						{t("panel.cap")}: <span className="tabular-nums">{effectiveCap(c) > 0 ? effectiveCap(c).toFixed(2) : t("panel.capNone")}</span>
					</span>
					<Input
						inputMode="decimal"
						placeholder={effectiveCap(c) > 0 ? effectiveCap(c).toFixed(2) : "0"}
						value={cap}
						onChange={(e) => setCap(e.target.value)}
						data-testid="live-cap"
					/>
					<span className="text-muted-foreground">
						{t("panel.capHint", { value: c.total_usd > 0 ? c.total_usd.toFixed(2) : t("panel.capNone") })}
					</span>
				</label>
				{c.models.length > 0 && (
					<fieldset className="flex flex-col gap-1 text-xs sm:col-span-2" data-testid="live-models">
						<legend className="font-medium">{t("panel.models")}</legend>
						<span className="text-muted-foreground">{t("panel.modelsHint")}</span>
						<div className="flex flex-wrap gap-3 pt-1">
							{c.models.map((m) => (
								<label key={m} className="flex items-center gap-1.5 font-mono">
									<input
										type="checkbox"
										checked={held.includes(m)}
										onChange={(e) => setHeld((h) => (e.target.checked ? [...h, m].sort() : h.filter((x) => x !== m)))}
									/>
									{m}
								</label>
							))}
						</div>
					</fieldset>
				)}
				<label className="flex flex-col gap-1 text-xs sm:col-span-2">
					<span className="font-medium">{t("reason")}</span>
					<Input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} data-testid="live-reason" />
				</label>
				<div className="flex items-center gap-2 sm:col-span-2">
					<Button type="submit" size="sm" disabled={!dirty || invalid} isLoading={state.isLoading} dataTestId="live-apply">
						{t("panel.save")}
					</Button>
					{state.isSuccess && !dirty && <span className="text-muted-foreground text-xs">{t("panel.saved")}</span>}
				</div>
				{state.error && <ErrorLine error={state.error} title={t("panel.saveError")} testId="live-settings-error" />}
			</form>
		</RoleGate>
	);
}

/** "Rerun failures": a new exploratory comparison of the failed tasks. Members and up. */
export function RerunFailuresButton({ expId }: { expId: string }) {
	const { t } = useTranslation("runControl");
	const [rerun, state] = useRerunFailuresMutation();
	const res = state.data;
	return (
		<RoleGate min="member" fallback={null}>
			<div className="flex min-w-0 flex-col gap-1.5" data-testid="rerun-failures">
				<Button
					variant="outline"
					size="sm"
					className="w-fit gap-1.5"
					title={t("rerun.hint")}
					isLoading={state.isLoading}
					onClick={() => void rerun({ expId })}
					dataTestId="rerun-failures-button"
				>
					<RotateCcw className="size-3.5" aria-hidden="true" />
					{t("rerun.button")}
				</Button>
				{res && (
					<span className="flex flex-wrap items-center gap-2 text-xs" role="status" data-testid="rerun-failures-started">
						{t("rerun.started", { id: res.experiment_id, tasks: res.tasks })}
						<Link
							to="/experiments/$expId"
							params={{ expId: res.experiment_id }}
							className="text-primary inline-flex items-center gap-1 font-medium hover:underline"
						>
							{t("rerun.open")}
							<ArrowRight className="size-3" aria-hidden="true" />
						</Link>
						{res.config_changed && res.config_changed.length > 0 && (
							<span className="text-muted-foreground">{t("rerun.configChanged", { arms: res.config_changed.join(", ") })}</span>
						)}
					</span>
				)}
				{state.error && <ErrorLine error={state.error} title={t("rerun.error")} testId="rerun-failures-error" />}
			</div>
		</RoleGate>
	);
}

/**
 * The run controls of an experiment: live settings while it runs, the error policy table, the
 * verified change log, and "Rerun failures" once it finished.
 */
export function RunControlsPanel({ expId, state }: { expId: string; state: string }) {
	const { t } = useTranslation("runControl");
	const controls = useGetRunControlsQuery(expId);
	const c = controls.data;
	if (!c) return null;
	const live = state === "running";
	const finished = state === "done" || state === "failed" || state === "cancelled";
	const cancelled = Object.entries(c.state.cancelled_slots ?? {});
	return (
		<section className="flex min-w-0 flex-col gap-3" data-testid="run-controls-panel" aria-label={t("panel.title")}>
			{c.exploratory && (
				<p className="bg-muted/60 rounded-lg border px-4 py-3 text-sm" role="note" data-testid="exploratory-banner">
					{t("panel.exploratory", { parent: c.rerun_of })}
				</p>
			)}
			<DetailsDisclosure label={t("panel.title")} meta={c.paused ? t("pausedBadge") : undefined} testId="run-controls-disclosure">
				<div className="flex min-w-0 flex-col gap-5 rounded-lg border p-4">
					<p className="text-muted-foreground text-xs">{t("panel.intro")}</p>
					{(live || c.paused) && <LiveSettings c={c} live={live} />}
					{cancelled.length > 0 && (
						<div className="text-xs" data-testid="cancelled-runs">
							<p className="font-medium">{t("panel.cancelled")}</p>
							<ul className="text-muted-foreground font-mono">
								{cancelled.map(([slot, h]) => (
									<li key={slot}>{h.run_id || slot}</li>
								))}
							</ul>
						</div>
					)}
					<PolicyTable rows={c.policy} />
					<ControlLog c={c} />
					{!c.exploratory && <RerunFailuresSlot c={c} finished={finished} expId={expId} />}
				</div>
			</DetailsDisclosure>
		</section>
	);
}

/**
 * Rerun failures where the server says it can work, else its reason as a muted line (fix wave 3, N285).
 * An older server without rerun_failures keeps the old rule: the button once the comparison finished.
 */
function RerunFailuresSlot({ c, finished, expId }: { c: RunControls; finished: boolean; expId: string }) {
	const rerun = c.rerun_failures; // absent on an older server
	if (!rerun) return finished ? <RerunFailuresButton expId={expId} /> : null;
	if (rerun.allowed) return <RerunFailuresButton expId={expId} />;
	if (!rerun.reason) return null;
	return (
		<p className="text-muted-foreground text-xs" data-testid="rerun-failures-reason">
			{rerun.reason}
		</p>
	);
}

/**
 * Cancel or requeue one run, for the run page. Cancel while the run is going; requeue when it
 * finished as aborted_infra or infra_error. A requeued run shows as replaced.
 */
export function RunActions({
	expId,
	runId,
	sealed,
	outcome,
	superseded,
}: {
	expId: string;
	runId: string;
	sealed: boolean;
	outcome?: string;
	superseded?: boolean;
}) {
	const { t } = useTranslation("runControl");
	const [cancel, cancelState] = useCancelRunMutation();
	const [requeue, requeueState] = useRequeueRunMutation();
	if (superseded) {
		return (
			<Badge variant="outline" data-testid="run-superseded">
				{t("run.superseded")}
			</Badge>
		);
	}
	const canCancel = !sealed;
	const canRequeue = sealed && requeueable(outcome);
	if (!canCancel && !canRequeue) return null;
	return (
		<RoleGate min="member" fallback={null}>
			<div className="flex flex-wrap items-center gap-2" data-testid="run-control-actions">
				{canCancel && (
					<ConfirmButton
						label={t("run.cancel")}
						question={t("run.confirmCancel", { id: runId })}
						confirmLabel={t("run.confirm")}
						cancelLabel={t("run.keep")}
						onConfirm={() => void cancel({ expId, runId })}
						isLoading={cancelState.isLoading}
						testId="run-cancel"
					/>
				)}
				{canRequeue && (
					<Button
						variant="outline"
						size="sm"
						className="h-7 gap-1.5 px-2.5 text-xs"
						title={t("run.requeueHint")}
						isLoading={requeueState.isLoading}
						disabled={requeueState.isSuccess}
						onClick={() => void requeue({ expId, runId })}
						dataTestId="run-requeue"
					>
						<RotateCcw className="size-3.5" aria-hidden="true" />
						{t("run.requeue")}
					</Button>
				)}
				{cancelState.error && <ErrorLine error={cancelState.error} title={t("run.cancelError")} testId="run-cancel-error" />}
				{requeueState.error && <ErrorLine error={requeueState.error} title={t("run.requeueError")} testId="run-requeue-error" />}
			</div>
		</RoleGate>
	);
}

/** The mutation cache key the rerun button and the page's notice share, so the notice can sit below the header. */
export function rerunCacheKey(evalId: string): string {
	return `evaluation-rerun-${evalId}`;
}

/**
 * "Started ev_... with n benchmarks. Open" after Rerun failures: a full-width notice below the evaluation's
 * header (fix wave 3, N193), so the header keeps its width.
 */
export function EvaluationRerunNotice({ evalId }: { evalId: string }) {
	const { t } = useTranslation("runControl");
	const [, state] = useRerunEvaluationFailuresMutation({ fixedCacheKey: rerunCacheKey(evalId) });
	const res = state.data;
	if (!res) return null;
	return (
		<div
			className="bg-muted/40 flex w-full flex-wrap items-center gap-2 rounded-lg border px-4 py-2 text-sm"
			role="status"
			data-testid="evaluation-rerun-started"
		>
			<span>{t("rerun.evalStarted", { id: res.evaluation.id, count: res.children.length })}</span>
			<Link
				to="/evaluations/$evalId"
				params={{ evalId: res.evaluation.id }}
				className="text-primary inline-flex items-center gap-1 font-medium hover:underline"
			>
				{t("rerun.open")}
				<ArrowRight className="size-3" aria-hidden="true" />
			</Link>
			<Button
				variant="ghost"
				size="sm"
				className="ml-auto h-7 px-2 text-xs"
				onClick={() => state.reset?.()}
				dataTestId="evaluation-rerun-started-close"
			>
				{t("rerun.close")}
			</Button>
		</div>
	);
}

/** Pause, resume and "Rerun failures" for an evaluation, beside its Cancel. */
export function EvaluationRunActions({ evaluation }: { evaluation: Evaluation }) {
	const { t } = useTranslation("runControl");
	const [pause, pauseState] = usePauseEvaluationMutation();
	const [resume, resumeState] = useResumeEvaluationMutation();
	const [rerun, rerunState] = useRerunEvaluationFailuresMutation({ fixedCacheKey: rerunCacheKey(evaluation.id) });
	const running = evaluation.status === "running";
	const finished = evaluation.status === "done" || evaluation.status === "failed" || evaluation.status === "cancelled";
	const paused = Boolean(evaluation.paused);
	return (
		<RoleGate min="member" fallback={null}>
			<div className="flex flex-wrap items-center gap-2" data-testid="evaluation-run-actions">
				{running && !paused && (
					<Button
						variant="outline"
						size="sm"
						className="gap-1.5"
						title={t("pauseHint")}
						isLoading={pauseState.isLoading}
						onClick={() => void pause({ evalId: evaluation.id })}
						dataTestId="evaluation-pause"
					>
						<Pause className="size-3.5" aria-hidden="true" />
						{t("pause")}
					</Button>
				)}
				{running && paused && (
					<>
						<Badge variant="outline" data-testid="evaluation-paused-chip">
							{t("pausedBadge")}
						</Badge>
						<Button
							variant="outline"
							size="sm"
							className="gap-1.5"
							isLoading={resumeState.isLoading}
							onClick={() => void resume({ evalId: evaluation.id })}
							dataTestId="evaluation-unpause"
						>
							<Play className="size-3.5" aria-hidden="true" />
							{t("resume")}
						</Button>
					</>
				)}
				{finished && !evaluation.rerun_of && hasFailedRuns(evaluation) && (
					<Button
						variant="outline"
						size="sm"
						className="gap-1.5"
						title={t("rerun.hint")}
						isLoading={rerunState.isLoading}
						onClick={() => void rerun({ evalId: evaluation.id })}
						dataTestId="evaluation-rerun-failures"
					>
						<RotateCcw className="size-3.5" aria-hidden="true" />
						{t("rerun.button")}
					</Button>
				)}
				{pauseState.error && <ErrorLine error={pauseState.error} title={t("pauseError")} testId="evaluation-pause-error" />}
				{resumeState.error && <ErrorLine error={resumeState.error} title={t("resumeError")} testId="evaluation-unpause-error" />}
				{rerunState.error && <ErrorLine error={rerunState.error} title={t("rerun.error")} testId="evaluation-rerun-error" />}
			</div>
		</RoleGate>
	);
}