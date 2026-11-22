import { RoleNote } from "@/components/platform/accessNotes";
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
} from "@/lib/store/apis/evalReleaseApi";
import type { EvaluationGateResult, GateCheck } from "@/lib/store/apis/evaluationsApi";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, CalendarClock, CheckCircle2, Rocket, ShieldAlert, ShieldQuestion } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { DatasetChange, ReleaseEvidence, versionName } from "./releaseEvidence";

/** The gate's pass or fail as a badge with its summary and each check. Shown on the report card. */
export function GateResultCard({ gate, testId = "evaluation-gate" }: { gate: EvaluationGateResult; testId?: string }) {
	const { t } = useTranslation("u14");
	const pass = gate.state === "pass";
	const fail = gate.state === "fail";
	const Icon = pass ? CheckCircle2 : fail ? ShieldAlert : ShieldQuestion;
	return (
		<section className="bg-card flex flex-col gap-3 rounded-xl border p-5" data-testid={testId} data-state={gate.state}>
			<div className="flex flex-wrap items-center gap-2">
				<Icon className={cn("size-4", pass ? "text-chart-success-ink" : fail ? "text-chart-error-ink" : "text-chart-warning-ink")} aria-hidden="true" />
				<h2 className="text-sm font-semibold">{t("promote.gateTitle")}</h2>
				<Badge variant={pass ? "success" : fail ? "destructive" : "warning"} data-testid={`${testId}-state`}>
					{t(`promote.gate.${gate.state}` as never, { defaultValue: gate.state })}
				</Badge>
				{gate.baseline && (
					<span className="text-muted-foreground text-xs">
						{t(gate.baseline.live ? "promote.comparedLive" : "promote.compared", { name: versionName(gate.baseline.agent, gate.baseline.version) })}
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
	const name = versionName(p.agent, p.version);
	return p.replaces_version ? t("releases.replaces", { name, prev: `v${p.replaces_version}` }) : t("releases.first", { name });
}

/**
 * "Promote this version" on a finished evaluation's report card: what the release would record
 * (agent version, the live version it replaces, before and after numbers, the gate), what blocks it,
 * and the approve form. One release covers every finished benchmark of the evaluation.
 */
export function PromoteEvaluation({ evaluationId }: { evaluationId: string }) {
	const { t } = useTranslation("u14");
	const session = useSession();
	const preview = useGetEvaluationReleasePreviewQuery(evaluationId);
	const [release, state] = useReleaseEvaluationMutation();
	const [open, setOpen] = useState(false);
	const [approver, setApprover] = useState("");
	const [note, setNote] = useState("");
	const [override, setOverride] = useState(false);
	const [reason, setReason] = useState("");
	const p = preview.data;
	if (!p || preview.isError) return null;
	const title = p.agent ? previewTitle(p, t as never) : t("promote.title");
	const blocked = p.blocked.length > 0;
	const canWrite = canUse(session, "member");
	const enabled = !p.problem && (!blocked || (override && reason.trim() !== "")) && !state.isLoading && (approver.trim() !== "" || session.signedIn);
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
							{p.live.version
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
			{p.problem && (
				<p className="text-muted-foreground text-sm" data-testid="evaluation-promote-problem">
					{p.problem}
				</p>
			)}
			{!p.problem && blocked && (
				<div className="border-destructive/40 bg-destructive/5 flex flex-col gap-1 rounded-md border px-3 py-2 text-sm" data-testid="evaluation-promote-blocked">
					{p.blocked.map((b) => (
						<p key={b} className="flex items-start gap-2">
							<AlertTriangle className="text-chart-error-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
							{b}
						</p>
					))}
				</div>
			)}
			{!canWrite && session.signedIn && <RoleNote min="member" testId="evaluation-promote-role" />}
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
								placeholder={session.user?.email ?? t("promote.approverPlaceholder")}
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
						<span className="text-muted-foreground text-xs">{t("promote.covers", { count: p.children.filter((c) => c.included).length })}</span>
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

/** "Repeat nightly" and "Repeat weekly": schedule a new evaluation of the same spec. */
export function RepeatEvaluation({ evaluationId }: { evaluationId: string }) {
	const { t } = useTranslation("u14");
	const session = useSession();
	const [schedule, state] = useScheduleEvaluationMutation();
	const [done, setDone] = useState<EvaluationCadence | null>(null);
	if (!canUse(session, "member")) return null;
	const go = async (cadence: EvaluationCadence) => {
		const res = await schedule({ id: evaluationId, cadence });
		if ("data" in res && res.data) setDone(cadence);
	};
	return (
		<span className="flex flex-wrap items-center gap-2" data-testid="evaluation-repeat">
			{done ? (
				<span className="text-muted-foreground text-xs" data-testid="evaluation-repeat-done">
					{t(`promote.repeatDone.${done}` as never)}{" "}
					<Link to="/schedules" className="text-primary hover:underline">
						{t("promote.viewSchedules")}
					</Link>
				</span>
			) : (
				<>
					{(["nightly", "weekly"] as const).map((c) => (
						<Button key={c} size="sm" variant="outline" disabled={state.isLoading} onClick={() => void go(c)} data-testid={`evaluation-repeat-${c}`}>
							<CalendarClock className="size-4" aria-hidden="true" />
							{t(`promote.repeat.${c}` as never)}
						</Button>
					))}
					{state.isError && (
						<span role="alert" className="text-chart-error-ink text-xs" data-testid="evaluation-repeat-error">
							{getBenchErrorMessage(state.error)}
						</span>
					)}
				</>
			)}
		</span>
	);
}
