import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { StatCard } from "@/components/ui/statCard";
import { StatusPill, type StatusTone } from "@/components/ui/statusPill";
import type {
	MaximFailureCluster,
	MaximFailureCounts,
	MaximFailureFilter,
	MaximReadinessStatus,
	MaximReviewRequest,
	MaximTaskDraft,
	MaximTaskState,
} from "@/lib/types/maximFailures";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** The pull form's fields, as typed. */
export interface FailureForm {
	evaluators: string;
	errors: boolean;
	scoreBelow: string;
	scoreEvaluators: string;
	days: string;
	limit: string;
}

export const DEFAULT_FAILURE_FORM: FailureForm = { evaluators: "", errors: true, scoreBelow: "", scoreEvaluators: "", days: "7", limit: "200" };

/** Comma-separated names, trimmed, without blanks or repeats. */
export function splitNames(s: string): string[] {
	return [...new Set(s.split(",").map((x) => x.trim()).filter(Boolean))];
}

/** The filter the form describes, or null when it names no rule. */
export function filterFromForm(f: FailureForm): MaximFailureFilter | null {
	const out: MaximFailureFilter = {};
	const evaluators = splitNames(f.evaluators);
	if (evaluators.length) out.evaluators = evaluators;
	if (f.errors) out.errors = true;
	const below = Number.parseFloat(f.scoreBelow);
	if (f.scoreBelow.trim() !== "" && Number.isFinite(below)) {
		out.score_below = below;
		const by = splitNames(f.scoreEvaluators);
		if (by.length) out.score_evaluators = by;
	}
	if (!out.evaluators && !out.errors && out.score_below === undefined) return null;
	const days = Number.parseInt(f.days, 10);
	if (Number.isFinite(days) && days > 0) out.days = days;
	const limit = Number.parseInt(f.limit, 10);
	if (Number.isFinite(limit) && limit > 0) out.limit = limit;
	return out;
}

export function stateTone(s: MaximTaskState): StatusTone {
	switch (s) {
		case "accepted":
			return "ok";
		case "needs_files":
			return "warn";
		case "rejected":
			return "bad";
		default:
			return "neutral";
	}
}

export function readinessTone(s: MaximReadinessStatus): StatusTone {
	switch (s) {
		case "ready":
			return "ok";
		case "not_ready":
			return "bad";
		case "queued":
			return "running";
		default:
			return "neutral";
	}
}

/** A question and answer task needs the right answer; other kinds a rubric. */
export function needsAnswer(kind: string): boolean {
	return kind === "qa" || kind === "memory";
}

/** Clusters that already have a drafted task in this import. */
export function draftedClusters(tasks: readonly MaximTaskDraft[]): Set<string> {
	return new Set(tasks.filter((t) => t.state !== "rejected").map((t) => t.cluster));
}

export function FailureCounts({ counts }: { counts: MaximFailureCounts }) {
	const { t } = useTranslation("maximFailures");
	const cells: [string, number][] = [
		[t("counts.scanned"), counts.scanned],
		[t("counts.failed"), counts.failed],
		[t("counts.errored"), counts.errored],
		[t("counts.evaluator"), counts.evaluator_failed],
		[t("counts.lowScore"), counts.low_score],
		[t("counts.newItems"), counts.new_items],
	];
	return (
		<div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6" data-testid="maxim-failure-counts">
			{cells.map(([label, value]) => (
				<StatCard key={label} label={label} value={value} />
			))}
		</div>
	);
}

export interface ClusterListProps {
	clusters: readonly MaximFailureCluster[];
	selected: ReadonlySet<string>;
	drafted: ReadonlySet<string>;
	onToggle(id: string): void;
}

export function ClusterList({ clusters, selected, drafted, onToggle }: ClusterListProps) {
	const { t } = useTranslation("maximFailures");
	if (clusters.length === 0) {
		return (
			<p className="text-muted-foreground rounded-xl border border-dashed px-6 py-8 text-center text-sm" data-testid="maxim-clusters-empty">
				{t("clusters.empty")}
			</p>
		);
	}
	return (
		<ul className="flex flex-col gap-2" data-testid="maxim-clusters">
			{clusters.map((c) => {
				const done = drafted.has(c.id);
				return (
					<li
						key={c.id}
						className={cn("bg-card flex min-w-0 gap-3 rounded-xl border p-4", selected.has(c.id) && "border-primary/60")}
						data-testid={`maxim-cluster-${c.id}`}
					>
						<input
							type="checkbox"
							className="mt-1 size-4 shrink-0"
							checked={selected.has(c.id)}
							disabled={done}
							onChange={() => onToggle(c.id)}
							aria-label={t("clusters.select", { label: c.label })}
						/>
						<div className="flex min-w-0 flex-1 flex-col gap-1">
							<div className="flex flex-wrap items-center gap-2">
								<span className="text-foreground font-medium break-words">{c.label}</span>
								<span className="text-muted-foreground text-xs tabular-nums">{t("clusters.traces", { count: c.occurrences })}</span>
								{done && <StatusPill tone="ok">{t("clusters.drafted")}</StatusPill>}
							</div>
							<p className="text-muted-foreground line-clamp-2 text-sm break-words">{c.sample}</p>
							{c.reasons && c.reasons.length > 0 && (
								<p className="text-muted-foreground text-xs break-words">{c.reasons.slice(0, 2).join("; ")}</p>
							)}
							{c.links && c.links.length > 0 && (
								<div className="flex flex-wrap gap-3 text-xs">
									{c.links.map((l) => (
										<a key={l} href={l} target="_blank" rel="noreferrer" className="text-primary underline-offset-2 hover:underline">
											{t("clusters.open")}
										</a>
									))}
								</div>
							)}
						</div>
					</li>
				);
			})}
		</ul>
	);
}

export interface TaskReviewCardProps {
	task: MaximTaskDraft;
	busy?: boolean;
	message?: string | null;
	onAccept(review: MaximReviewRequest): void;
	onReject(reason: string): void;
	onRerun(): void;
}

const fieldClass = "border-input bg-background min-h-20 w-full rounded-md border px-3 py-2 text-sm";

/** One drafted task: what happened in production, the review form, and its readiness. */
export function TaskReviewCard({ task, busy = false, message = null, onAccept, onReject, onRerun }: TaskReviewCardProps) {
	const { t } = useTranslation("maximFailures");
	const [answer, setAnswer] = useState("");
	const [rubric, setRubric] = useState(task.suggested_rubric);
	const [instruction, setInstruction] = useState("");
	const [reason, setReason] = useState("");
	const open = task.state === "pending_review" || task.state === "needs_files";
	const qa = needsAnswer(task.kind);
	return (
		<article className="bg-card flex min-w-0 flex-col gap-3 rounded-xl border p-4" data-testid={`maxim-task-${task.id}`}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="font-mono text-sm break-all">
					{task.suite}/{task.id}
				</span>
				<StatusPill tone={stateTone(task.state)} testId="maxim-task-state">
					{t(`state.${task.state}`)}
				</StatusPill>
				{task.state === "accepted" && (
					<StatusPill tone={readinessTone(task.readiness.status)} testId="maxim-task-readiness">
						{t(`readiness.${task.readiness.status}`)}
					</StatusPill>
				)}
				{task.state !== "rejected" && (
					<Link to="/suites/$suiteId" params={{ suiteId: task.suite }} className="text-primary ml-auto text-xs underline-offset-2 hover:underline">
						{t("tasks.openTask")}
					</Link>
				)}
			</div>
			<dl className="grid gap-2 text-sm sm:grid-cols-2">
				<div className="min-w-0">
					<dt className="text-muted-foreground text-xs">{t("tasks.request")}</dt>
					<dd className="break-words">{task.request}</dd>
				</div>
				<div className="min-w-0">
					<dt className="text-muted-foreground text-xs">{t("tasks.recorded")}</dt>
					<dd className="break-words">{task.recorded || "-"}</dd>
				</div>
			</dl>
			{task.generated.length > 0 && <p className="text-muted-foreground text-xs">{t("tasks.generated", { ids: task.generated.join(", ") })}</p>}
			{task.edit_me && task.edit_me.length > 0 && task.state === "needs_files" && (
				<p className="text-xs text-amber-700 dark:text-amber-400" data-testid="maxim-task-editme">
					{t("tasks.editMe", { files: task.edit_me.join(", ") })}
				</p>
			)}
			{task.reviewed_by && <p className="text-muted-foreground text-xs">{t("tasks.reviewedBy", { who: task.reviewed_by })}</p>}
			{task.state === "pending_review" && (
				<form
					className="flex flex-col gap-3"
					onSubmit={(e) => {
						e.preventDefault();
						onAccept({ answer: answer.trim() || undefined, rubric: rubric.trim() || undefined, instruction: instruction.trim() || undefined });
					}}
				>
					{qa && (
						<label className="flex flex-col gap-1 text-sm">
							<span className="font-medium">{t("tasks.answer")}</span>
							<textarea className={fieldClass} value={answer} onChange={(e) => setAnswer(e.target.value)} data-testid="maxim-task-answer" />
							<span className="text-muted-foreground text-xs">{t("tasks.answerHint")}</span>
						</label>
					)}
					<label className="flex flex-col gap-1 text-sm">
						<span className="font-medium">{t("tasks.rubric")}</span>
						<textarea className={fieldClass} value={rubric} onChange={(e) => setRubric(e.target.value)} data-testid="maxim-task-rubric" />
					</label>
					<label className="flex flex-col gap-1 text-sm">
						<span className="font-medium">{t("tasks.instruction")}</span>
						<textarea className={fieldClass} value={instruction} placeholder={task.request} onChange={(e) => setInstruction(e.target.value)} />
						<span className="text-muted-foreground text-xs">{t("tasks.instructionHint")}</span>
					</label>
					<div className="flex flex-wrap items-center gap-2">
						<Button type="submit" size="sm" disabled={busy || (qa && !answer.trim())} data-testid="maxim-task-accept">
							{t("tasks.accept")}
						</Button>
					</div>
				</form>
			)}
			{(task.state === "accepted" || task.state === "needs_files") && task.readiness.status !== "ready" && (
				<div className="flex flex-wrap gap-2">
					<Button type="button" size="sm" variant="outline" disabled={busy} onClick={onRerun} data-testid="maxim-task-rerun">
						{t("tasks.readinessAgain")}
					</Button>
					{task.readiness.job_id && (
						<Link to="/jobs" className="text-primary self-center text-xs underline-offset-2 hover:underline">
							{t("tasks.openJob")}
						</Link>
					)}
				</div>
			)}
			{open && (
				<div className="flex flex-wrap items-center gap-2">
					<Input
						className="max-w-sm"
						value={reason}
						placeholder={t("tasks.rejectReason")}
						onChange={(e) => setReason(e.target.value)}
						data-testid="maxim-task-reject-reason"
					/>
					<Button type="button" size="sm" variant="outline" disabled={busy || !reason.trim()} onClick={() => onReject(reason.trim())} data-testid="maxim-task-reject">
						{t("tasks.reject")}
					</Button>
				</div>
			)}
			{task.note && <p className="text-muted-foreground text-xs">{task.note}</p>}
			{message && (
				<p className="text-sm" role="status" data-testid="maxim-task-message">
					{message}
				</p>
			)}
		</article>
	);
}
