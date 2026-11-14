import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import { useGetSuiteTaskQuery } from "@/lib/store";
import type { SuiteTaskDetail } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { CircleCheck, CircleDashed, CircleX, EyeOff, X } from "lucide-react";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { AdmissionBadge } from "./suiteBadges";
import { controlRows, formatBytes, type ControlRow } from "./suites.utils";

export interface TaskDetailPanelProps {
	suiteId: string;
	suiteVersion: string;
	taskId: string;
	onClose: () => void;
}

/**
 * The open task: fetched on demand, so the list stays light. Focus moves to the heading when a
 * task opens, and Escape closes the panel.
 */
export function TaskDetailPanel({ suiteId, suiteVersion, taskId, onClose }: TaskDetailPanelProps) {
	const { t } = useTranslation("bench");
	const query = useGetSuiteTaskQuery({ suite: suiteId, task: taskId });
	const heading = useRef<HTMLHeadingElement>(null);
	useEffect(() => {
		heading.current?.focus();
	}, [suiteId, taskId]);

	return (
		<section
			className="bg-card flex min-w-0 flex-col gap-4 rounded-sm border p-4"
			aria-labelledby="suites-task-detail-heading"
			data-testid="suites-task-detail"
			onKeyDown={(event) => {
				if (event.key === "Escape") onClose();
			}}
		>
			<div className="flex items-start justify-between gap-3">
				<h2
					id="suites-task-detail-heading"
					ref={heading}
					tabIndex={-1}
					className="text-foreground min-w-0 text-sm font-semibold tracking-tight break-words focus:outline-none"
				>
					{query.data ? `${query.data.task.id}: ${query.data.task.title}` : t("suites.detail.label", { task: taskId })}
				</h2>
				<Button variant="ghost" size="sm" onClick={onClose} aria-label={t("suites.detail.close")} dataTestId="suites-task-detail-close">
					<X aria-hidden="true" />
				</Button>
			</div>
			{query.isLoading ? (
				<BlockSkeleton rows={6} testId="suites-task-detail-loading" />
			) : query.isError || !query.data ? (
				<LoadError
					title={t("suites.detail.loadError")}
					error={query.error}
					onRetry={() => void query.refetch()}
					testId="suites-task-detail-error"
				/>
			) : (
				<TaskDetailBody detail={query.data} suiteVersion={suiteVersion} />
			)}
		</section>
	);
}

/** The task's instruction, provenance, admission checklist, hidden counts, protected paths and files. */
export function TaskDetailBody({ detail, suiteVersion }: { detail: SuiteTaskDetail; suiteVersion: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const task = detail.task;
	return (
		<div className="flex min-w-0 flex-col gap-5">
			<div className="flex flex-wrap items-center gap-2">
				<AdmissionBadge task={task} testId="suites-task-detail-admission" />
				<span className="text-muted-foreground font-mono text-xs">
					{task.kind} / v{task.version}
				</span>
			</div>

			<DetailBlock title={t("suites.detail.instruction")} hint={t("suites.detail.instructionHint")} testId="suites-task-instruction">
				{detail.instruction ? (
					// Plain text on purpose: the instruction is author-written markdown and is never parsed as HTML.
					<pre className="bg-muted/50 text-foreground max-h-96 min-w-0 overflow-auto rounded-sm border px-3 py-2 font-sans text-xs leading-relaxed break-words whitespace-pre-wrap">
						{detail.instruction}
					</pre>
				) : (
					<p className="text-muted-foreground text-xs">{t("suites.detail.instructionEmpty")}</p>
				)}
				{detail.instruction_truncated && <p className="text-muted-foreground text-xs">{t("suites.detail.instructionTruncated")}</p>}
			</DetailBlock>

			<DetailBlock title={t("suites.detail.provenance")} testId="suites-task-provenance">
				<Provenance detail={detail} />
			</DetailBlock>

			<DetailBlock
				title={t("suites.detail.admission")}
				hint={
					task.admitted_at
						? `${t("suites.detail.admissionHint")} ${t("suites.detail.admittedAt", { when: formatDateTime(task.admitted_at, locale) ?? task.admitted_at })}`
						: t("suites.detail.admissionHint")
				}
				testId="suites-task-checklist"
			>
				{task.checks.length === 0 ? (
					<p className="text-muted-foreground text-xs">{t("suites.detail.neverAdmitted")}</p>
				) : (
					<ul className="flex min-w-0 flex-col gap-1.5">
						{controlRows(task.checks).map((row) => (
							<ControlItem key={row.control} row={row} mutants={task.mutants} />
						))}
					</ul>
				)}
			</DetailBlock>

			<div
				role="note"
				className="border-chart-info/50 bg-chart-info/10 flex items-start gap-2 rounded-sm border px-3 py-2 text-xs"
				data-testid="suites-task-hidden"
			>
				<EyeOff className="text-chart-info-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
				<div className="flex min-w-0 flex-col gap-0.5">
					<span className="text-foreground font-medium">{t("suites.detail.hidden")}</span>
					<span className="text-muted-foreground">
						{t("suites.detail.hiddenBody", { files: task.hidden_test_files, required: task.required_tests })}
					</span>
				</div>
			</div>

			<DetailBlock title={t("suites.detail.protected")} testId="suites-task-protected">
				{detail.protected.length === 0 ? (
					<p className="text-muted-foreground text-xs">{t("suites.detail.protectedNone")}</p>
				) : (
					<>
						<p className="text-muted-foreground text-xs">{t("suites.detail.protectedBody", { count: detail.protected.length })}</p>
						<ul className="flex flex-wrap gap-1">
							{detail.protected.map((glob) => (
								<li key={glob} className="bg-muted rounded-sm px-1.5 py-0.5 font-mono text-[11px]">
									{glob}
								</li>
							))}
						</ul>
					</>
				)}
			</DetailBlock>

			<DetailBlock title={t("suites.detail.files")} hint={t("suites.detail.filesHint")} testId="suites-task-files">
				{detail.files.length === 0 ? (
					<p className="text-muted-foreground text-xs">{t("suites.detail.filesEmpty")}</p>
				) : (
					<ul className="max-h-64 min-w-0 overflow-auto rounded-sm border text-xs">
						{detail.files.map((file) => (
							<li key={file.path} className="flex min-w-0 items-center justify-between gap-3 border-b px-2 py-1 last:border-b-0">
								<span className="text-foreground min-w-0 truncate font-mono" title={file.path}>
									{file.path}
								</span>
								<span className="text-muted-foreground shrink-0 tabular-nums">{formatBytes(file.size, locale)}</span>
							</li>
						))}
					</ul>
				)}
				{detail.files_truncated && (
					<p className="text-muted-foreground text-xs">
						{t("suites.detail.filesTruncated", { shown: detail.files.length, total: detail.files_total })}
					</p>
				)}
			</DetailBlock>

			<DetailBlock title={t("suites.detail.runs")} hint={t("suites.detail.runsHint")} testId="suites-task-runs">
				<Link
					to="/leaderboard"
					search={{ suite: `${detail.suite}@${detail.suite_version || suiteVersion}` }}
					className="text-primary w-fit text-xs underline-offset-4 hover:underline"
					data-testid="suites-task-leaderboard-link"
				>
					{t("suites.facts.leaderboard")}
				</Link>
			</DetailBlock>
		</div>
	);
}

function DetailBlock({ title, hint, children, testId }: { title: string; hint?: string; children: React.ReactNode; testId: string }) {
	return (
		<section className="flex min-w-0 flex-col gap-1.5" data-testid={testId}>
			<h3 className="text-foreground text-xs font-semibold tracking-wide uppercase">{title}</h3>
			{hint && <p className="text-muted-foreground text-xs">{hint}</p>}
			{children}
		</section>
	);
}

function Provenance({ detail }: { detail: SuiteTaskDetail }) {
	const { t } = useTranslation("bench");
	const p = detail.task.provenance;
	if (!p) return <p className="text-muted-foreground text-xs">{t("suites.detail.provenanceNone")}</p>;
	const rows: [string, string | undefined, string][] = [
		[t("suites.detail.source"), p.source, "source"],
		[t("suites.detail.baseCommit"), p.base_commit, "base"],
		[t("suites.detail.referenceCommit"), p.reference_commit, "reference"],
		[t("suites.detail.license"), p.license, "license"],
		[t("suites.detail.importedFrom"), p.imported_from, "imported"],
		[t("suites.detail.createdBy"), p.created_by, "created"],
	];
	return (
		<dl className="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
			{rows.map(([label, value, key]) => (
				<div key={key} className="contents" data-testid={`suites-provenance-${key}`}>
					<dt className="text-muted-foreground">{label}</dt>
					<dd
						className={cn(
							"min-w-0 font-mono break-all",
							value ? "text-foreground" : key === "license" ? "text-chart-warning-ink" : "text-muted-foreground",
						)}
					>
						{value || (key === "license" ? t("suites.licenses.missing") : t("suites.facts.none"))}
					</dd>
				</div>
			))}
		</dl>
	);
}

function ControlItem({ row, mutants }: { row: ControlRow; mutants: number }) {
	const { t } = useTranslation("bench");
	const allPassed = row.recorded && row.passed === row.checks.length;
	const noMutants = row.control === "mutant" && !row.recorded && mutants === 0;
	const Icon = !row.recorded ? CircleDashed : allPassed ? CircleCheck : CircleX;
	const tone = !row.recorded ? "text-muted-foreground" : allPassed ? "text-chart-success-ink" : "text-chart-error-ink";
	const state = !row.recorded ? t("suites.detail.notRecorded") : allPassed ? t("suites.detail.pass") : t("suites.detail.fail");
	return (
		<li
			className="flex min-w-0 flex-col gap-0.5 rounded-sm border px-2 py-1.5"
			data-testid={`suites-control-${row.control}`}
			data-state={!row.recorded ? "not_recorded" : allPassed ? "pass" : "fail"}
		>
			<span className="flex min-w-0 items-start gap-2 text-xs">
				<Icon className={cn("mt-0.5 size-3.5 shrink-0", tone)} aria-hidden="true" />
				<span className="text-foreground min-w-0 flex-1">{t(`suites.controls.${row.control}`)}</span>
				<span className={cn("shrink-0 font-medium", tone)}>
					<span className="sr-only">: </span>
					{state}
					{row.recorded && row.checks.length > 1 && (
						<span className="text-muted-foreground ml-1 font-normal">
							{t("suites.detail.controlPassed", { passed: row.passed, total: row.checks.length })}
						</span>
					)}
					{noMutants && <span className="text-muted-foreground ml-1 font-normal">({t("suites.controls.mutantNone")})</span>}
				</span>
			</span>
			{row.checks.length > 0 && (
				<ul className="flex min-w-0 flex-col gap-0.5 pl-5.5">
					{row.checks.map((check) => (
						<li
							key={check.name}
							className="text-muted-foreground flex min-w-0 flex-wrap gap-x-2 font-mono text-[11px]"
							data-testid={`suites-check-${check.name}`}
						>
							<span className={check.passed ? "text-foreground" : "text-chart-error-ink"}>{check.name}</span>
							<span>{t("suites.detail.want", { want: check.want, got: check.got })}</span>
							{check.summary && <span className="break-all">{check.summary}</span>}
						</li>
					))}
				</ul>
			)}
		</li>
	);
}