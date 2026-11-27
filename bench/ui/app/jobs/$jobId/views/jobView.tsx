import { RoleGate } from "@/components/platform/accessNotes";
import { ConfirmButton, JobStatusBadge } from "@/components/platform/badges";
import { ForbiddenNote, isRoleRefusal } from "@/components/platform/forbiddenNote";
import { JobProgressBar, useJobKindLabel } from "@/components/platform/jobsDrawer";
import {
	durationSeconds,
	formatDuration,
	isActiveJob,
	jobExperimentId,
	jobProgress,
	linkifyExperiments,
	targetLabel,
	toDate,
} from "@/components/platform/platform.utils";
import { useJobLog, type JobLogState } from "@/components/platform/useJobLog";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { When } from "@/components/settings/settingsBits";
import { Button } from "@/components/ui/button";
import { CopyId } from "@/components/ui/copyId";
import { getLocale } from "@/lib/i18n";
import { useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCancelJobMutation, useGetJobQuery } from "@/lib/store/apis/platformApi";
import { isMissingRoute } from "@/lib/store/apis/session";
import type { Job, JobLogLine } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, ArrowDownToLine, FlaskConical, Gavel } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** How often the job page refreshes the job while it is queued or running. */
export const JOB_POLL_MS = 2000;

/** The regrade id a finished regrade job wrote, from its result or params. */
export function regradeIdOf(job: Pick<Job, "kind" | "result" | "params">): string | null {
	if (job.kind !== "experiment.regate") return null;
	for (const source of [job.result, job.params]) {
		if (!source) continue;
		for (const key of ["regrade_id", "id"]) {
			const value = source[key];
			if (typeof value === "string" && value) return value;
		}
	}
	return null;
}

/**
 * One job: what it is, how far it got, its live log, its result or error, and cancel while it can
 * still be stopped.
 */
export default function JobView({ jobId }: { jobId: string }) {
	const { t } = useTranslation("platform");
	const [active, setActive] = useState(true);
	const job = useGetJobQuery(jobId, { pollingInterval: active ? JOB_POLL_MS : 0, skipPollingIfUnfocused: true });
	const log = useJobLog(jobId);
	const nowActive = job.data ? isActiveJob(job.data) : true;
	useEffect(() => setActive(nowActive), [nowActive]);

	if (job.isLoading) return <BlockSkeleton rows={8} testId="job-loading" />;
	if (job.isError || !job.data) {
		if (isMissingRoute(job.error)) {
			return (
				<section className="flex flex-col gap-4" data-testid="job-screen">
					<h1 className="text-foreground">{t("jobs.page.title", { id: jobId })}</h1>
					<p className="text-muted-foreground text-sm" data-testid="job-not-found">
						{t("jobs.page.notFound", { id: jobId })}
					</p>
				</section>
			);
		}
		return (
			<LoadError title={t("jobs.page.loadError", { id: jobId })} error={job.error} onRetry={() => void job.refetch()} testId="job-error" />
		);
	}
	return <JobDetail job={job.data} log={log} />;
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<dt className="text-muted-foreground text-[11px] font-medium tracking-[0.06em] uppercase">{label}</dt>
			<dd className="text-foreground min-w-0 truncate text-sm">{children}</dd>
		</div>
	);
}

export function JobDetail({ job, log }: { job: Job; log: JobLogState }) {
	const { t } = useTranslation("platform");
	const kindLabel = useJobKindLabel();
	const session = useSession();
	const [cancel, cancelState] = useCancelJobMutation();
	const activeJob = isActiveJob(job);
	const progress = jobProgress(job);
	const expId = jobExperimentId(job);
	const regradeId = regradeIdOf(job);
	const target = targetLabel(job.target);
	// An experiment job opens its result; any other job that ran a comparison (a benchmark's
	// readiness controls) opens that comparison.
	const experimentJob = job.kind.startsWith("experiment.");
	const duration = formatDuration(durationSeconds(job.started_at, job.finished_at));

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="job-screen" data-status={job.status}>
			<header className="flex flex-col gap-3">
				<div className="text-muted-foreground flex items-center gap-2 text-xs">
					<span>{t("jobs.drawerTitle")}</span>
					<span aria-hidden="true">/</span>
					<CopyId value={job.id} className="max-w-64" testId="job-id" />
				</div>
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground">{target.text ? `${kindLabel(job.kind)} of ${target.text}` : kindLabel(job.kind)}</h1>
					<JobStatusBadge status={job.status} />
					{job.cancel_requested && activeJob && (
						<span className="text-chart-warning-ink text-xs font-medium" data-testid="job-cancel-requested">
							{t("jobs.cancelRequested")}
						</span>
					)}
					<div className="ml-auto flex flex-wrap items-center gap-2">
						{expId && !experimentJob && (
							<Button asChild variant="outline" size="sm" className="gap-1.5">
								<Link to="/experiments/$expId" params={{ expId }} data-testid="job-open-comparison">
									<FlaskConical className="size-3.5" aria-hidden="true" />
									{t("jobs.page.openComparison")}
								</Link>
							</Button>
						)}
						{expId && experimentJob && (
							<Button asChild variant="outline" size="sm" className="gap-1.5">
								<Link
									to="/verdict"
									search={regradeId && job.status === "succeeded" ? { exp: expId, grade: regradeId } : { exp: expId }}
									data-testid="job-open-verdict"
								>
									<Gavel className="size-3.5" aria-hidden="true" />
									{t("jobs.page.openVerdict")}
								</Link>
							</Button>
						)}
						{activeJob && !job.cancel_requested && (
							<RoleGate min="member">
								<ConfirmButton
									label={t("jobs.page.cancel")}
									question={t("jobs.page.confirmCancel")}
									confirmLabel={t("jobs.page.confirm")}
									cancelLabel={t("jobs.page.keep")}
									onConfirm={() => void cancel(job.id)}
									isLoading={cancelState.isLoading}
									variant="destructive"
									className="h-8 px-3 text-sm"
									testId="job-cancel"
								/>
							</RoleGate>
						)}
					</div>
				</div>
				{cancelState.error &&
					(isRoleRefusal(session, cancelState.error) ? (
						<ForbiddenNote min="member" error={cancelState.error} testId="job-cancel-error" />
					) : (
						<p role="alert" className="text-chart-error-ink text-xs" data-testid="job-cancel-error">
							{t("jobs.page.cancelError")}: {getBenchErrorMessage(cancelState.error)}
						</p>
					))}
			</header>

			<dl className="grid grid-cols-2 gap-x-6 gap-y-4 rounded-lg border px-5 py-4 sm:grid-cols-3 lg:grid-cols-6" data-testid="job-facts">
				<Fact label={t("jobs.page.target")}>
					{target.to ? (
						<a href={target.to} className="text-primary text-xs underline underline-offset-2" title={job.target} data-testid="job-target">
							{target.text}
						</a>
					) : (
						<span className="text-xs" title={job.target} data-testid="job-target">
							{target.text || "-"}
						</span>
					)}
				</Fact>
				<Fact label={t("jobs.page.created")}>
					<When value={job.created_at} />
				</Fact>
				<Fact label={t("jobs.page.started")}>
					<When value={job.started_at} empty="-" />
				</Fact>
				<Fact label={t("jobs.page.finished")}>
					<When value={job.finished_at} empty="-" />
				</Fact>
				<Fact label={t("jobs.page.duration")}>
					<span className="tabular-nums">{duration ?? "-"}</span>
				</Fact>
				<Fact label={t("jobs.page.createdBy")}>
					{job.created_by && job.created_by === session.user?.id ? (
						<span className="text-xs">{session.user.name || session.user.email}</span>
					) : (
						<span
							className={cn("text-xs", !job.created_by_name && "font-mono")}
							title={job.created_by || undefined}
							data-testid="job-created-by"
						>
							{job.created_by_name || job.created_by || "-"}
						</span>
					)}
					{job.attempt > 1 && <span className="text-muted-foreground ml-2 text-xs">{t("jobs.attempt", { n: job.attempt })}</span>}
				</Fact>
			</dl>

			{(activeJob || progress) && (
				<div className="flex flex-col gap-2" data-testid="job-progress">
					<div className="flex items-baseline justify-between gap-2 text-xs">
						<span className="text-muted-foreground font-medium tracking-[0.06em] uppercase">{t("jobs.page.progress")}</span>
						<span className="text-foreground tabular-nums">
							{progress && progress.total > 0 ? t("jobs.page.progressOf", { done: progress.done, total: progress.total }) : null}
							{progress?.label && <span className="text-muted-foreground ml-2">{progress.label}</span>}
						</span>
					</div>
					<JobProgressBar job={job} className="h-2" />
				</div>
			)}

			{job.status === "failed" && job.error && (
				<div
					role="alert"
					className="border-chart-error/40 bg-chart-error/5 flex items-start gap-3 rounded-lg border px-4 py-3"
					data-testid="job-error-block"
				>
					<AlertTriangle className="text-chart-error-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					<div className="flex min-w-0 flex-col gap-1">
						<span className="text-foreground text-sm font-medium">{t("jobs.page.error")}</span>
						<pre className="text-muted-foreground font-mono text-xs break-words whitespace-pre-wrap">{job.error}</pre>
					</div>
				</div>
			)}

			<JobLogPanel log={log} active={activeJob} />

			<div className="grid gap-4 lg:grid-cols-2">
				{job.result && Object.keys(job.result).length > 0 && (
					<JsonBlock title={t("jobs.page.result")} value={job.result} testId="job-result" />
				)}
				{job.params && Object.keys(job.params).length > 0 && (
					<JsonBlock title={t("jobs.page.params")} value={job.params} testId="job-params" />
				)}
			</div>
		</section>
	);
}

function JsonBlock({ title, value, testId }: { title: string; value: unknown; testId: string }) {
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid={testId}>
			<h2 className="text-foreground text-[15px] font-semibold">{title}</h2>
			<pre className="bench-mono-block max-h-80 overflow-auto px-3 py-2.5">{JSON.stringify(value, null, 2)}</pre>
		</div>
	);
}

const LEVEL_CLASS: Record<string, string> = {
	error: "text-chart-error",
	warn: "text-chart-warning",
	warning: "text-chart-warning",
	debug: "text-zinc-500",
};

/** A log line's time of day in the reader's locale, 24-hour, to the second. */
function timeOf(at: JobLogLine["at"]): string {
	const date = toDate(at);
	if (!date) return "";
	return new Intl.DateTimeFormat(getLocale(), { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" }).format(date);
}

/** The live log: one row per line, following the bottom unless the reader scrolled up. */
export function JobLogPanel({ log, active }: { log: JobLogState; active: boolean }) {
	const { t } = useTranslation("platform");
	const scroller = useRef<HTMLDivElement>(null);
	const [follow, setFollow] = useState(true);
	const live = active && !log.ended;

	useEffect(() => {
		const el = scroller.current;
		if (follow && el) el.scrollTop = el.scrollHeight;
	}, [log.lines.length, follow]);

	return (
		<section
			className="flex min-w-0 flex-col overflow-hidden rounded-xl border border-zinc-800 bg-zinc-950 text-zinc-100 shadow-sm"
			data-testid="job-log"
			data-transport={log.transport}
		>
			<div className="flex items-center gap-3 border-b border-zinc-800 bg-zinc-900/80 px-3 py-2">
				<span className="flex items-center gap-1.5" aria-hidden="true">
					<span className="size-2.5 rounded-full bg-zinc-700" />
					<span className="size-2.5 rounded-full bg-zinc-700" />
					<span className="size-2.5 rounded-full bg-zinc-700" />
				</span>
				<h2 className="text-xs font-medium text-zinc-300">{t("jobs.page.log")}</h2>
				<span
					className={cn("flex items-center gap-1.5 text-xs font-medium", live ? "text-chart-success" : "text-zinc-500")}
					data-testid={live ? "job-log-live" : "job-log-done"}
				>
					<span className="relative inline-flex size-1.5" aria-hidden="true">
						{live && <span className="bg-chart-success absolute inline-flex size-full animate-ping rounded-full opacity-70" />}
						<span className={cn("relative inline-flex size-1.5 rounded-full", live ? "bg-chart-success" : "bg-zinc-600")} />
					</span>
					{live ? t("jobs.page.logLive") : t("jobs.page.logDone")}
				</span>
				<span className="ml-auto text-[11px] text-zinc-500 tabular-nums">{log.lines.length}</span>
				{!follow && (
					<Button
						variant="ghost"
						size="sm"
						className="h-6 gap-1 px-2 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
						onClick={() => setFollow(true)}
						dataTestId="job-log-follow"
					>
						<ArrowDownToLine className="size-3.5" aria-hidden="true" />
						{t("jobs.page.follow")}
					</Button>
				)}
			</div>
			{log.dropped > 0 && (
				<p className="border-b border-zinc-800 px-3 py-1 text-xs text-zinc-500" data-testid="job-log-dropped">
					{t("jobs.page.logDropped", { count: log.dropped })}
				</p>
			)}
			<div
				ref={scroller}
				onScroll={(event) => {
					const el = event.currentTarget;
					setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 24);
				}}
				className="max-h-[min(60vh,32rem)] min-h-40 overflow-auto py-2 font-mono text-xs leading-5"
				role="log"
				aria-live={live ? "polite" : "off"}
				aria-label={t("jobs.page.log")}
			>
				{log.lines.length === 0 ? (
					<p className="px-3 py-1 text-zinc-500" data-testid="job-log-empty">
						{t("jobs.page.logEmpty")}
					</p>
				) : (
					<ol className="flex flex-col">
						{log.lines.map((line) => (
							<li
								key={line.seq}
								className="grid grid-cols-[2.5rem_4.5rem_minmax(0,1fr)] gap-3 px-3 hover:bg-zinc-900"
								data-testid={`job-log-line-${line.seq}`}
							>
								<span className="text-right text-zinc-500 tabular-nums select-none" data-testid={`job-log-seq-${line.seq}`}>
									{line.seq}
								</span>
								<span className="text-zinc-500 tabular-nums select-none">{timeOf(line.at)}</span>
								<span className={cn("break-words whitespace-pre-wrap", LEVEL_CLASS[line.level])}>
									<LogText text={line.line} />
								</span>
							</li>
						))}
						{live && (
							<li className="px-3 pl-[8.5rem]" aria-hidden="true">
								<span className="inline-block h-3.5 w-1.5 animate-pulse bg-zinc-400 align-middle" />
							</li>
						)}
					</ol>
				)}
			</div>
			{log.error !== undefined && log.lines.length === 0 && (
				<p className="text-chart-error border-t border-zinc-800 px-3 py-2 text-xs" role="alert">
					{getBenchErrorMessage(log.error)}
				</p>
			)}
		</section>
	);
}

/** A log line with every comparison id (e_...) linked to the comparison's page. */
export function LogText({ text }: { text: string }) {
	return (
		<>
			{linkifyExperiments(text).map((part, i) =>
				typeof part === "string" ? (
					part
				) : (
					<Link
						key={i}
						to="/experiments/$expId"
						params={{ expId: part.exp }}
						className="text-sky-300 underline underline-offset-2 hover:text-sky-200"
						data-testid="job-log-exp-link"
					>
						{part.exp}
					</Link>
				),
			)}
		</>
	);
}