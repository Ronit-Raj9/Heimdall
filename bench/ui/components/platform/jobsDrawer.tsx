import { jobKindLabel, jobKindLabelFor } from "@/components/jobs/jobKindLabels";
import { JobStatusBadge } from "@/components/platform/badges";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Dialog } from "@/components/ui/dialog";
import { getLocale } from "@/lib/i18n";
import { addUnseenDone, clearUnseenDone, notify } from "@/lib/notify";
import { useSession } from "@/lib/session/sessionContext";
import { JOBS_POLL_MS, useListJobsQuery } from "@/lib/store/apis/platformApi";
import { errorStatus } from "@/lib/store/apis/session";
import type { Job } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ChevronRight, ListTodo } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { formatRelative, isActiveJob, jobFraction, shortJobId, splitJobs, targetLabel } from "./platform.utils";

/** How often the indicator refreshes when nothing is running and the drawer is closed. */
const IDLE_POLL_MS = 30_000;
/** Finished jobs listed in the drawer. */
const RECENT_LIMIT = 12;

/**
 * Jobs that were active at the last poll and are finished now: the finish notices (M1-45).
 * The first poll (prev null) reports nothing, so opening a page never shows old finishes.
 */
export function finishedSince(prev: ReadonlySet<string> | null, jobs: readonly Job[]): Job[] {
	if (!prev) return [];
	return jobs.filter((j) => prev.has(j.id) && !isActiveJob(j));
}

/** A job's kind in words ("experiment.run" is jobs.kinds.experiment_run); then the shared kind map (jobKindLabels.ts); unknown kinds show as written. */
export function useJobKindLabel(): (kind: string) => string {
	const { t, i18n } = useTranslation("platform");
	return (kind) => {
		const key = `jobs.kinds.${kind.replace(/\./g, "_")}`;
		return i18n.exists(key, { ns: "platform" }) ? t(key as "jobs.kinds.suite_lint") : jobKindLabel(kind);
	};
}

/** A thin progress bar: determinate when the job reports done of total, a moving stripe otherwise. */
export function JobProgressBar({ job, className }: { job: Job; className?: string }) {
	const fraction = jobFraction(job);
	const active = isActiveJob(job);
	if (!active && fraction === null) return null;
	return (
		<div
			className={cn("bg-chart-track relative h-1.5 w-full overflow-hidden rounded-full", className)}
			role="progressbar"
			aria-valuemin={0}
			aria-valuemax={100}
			aria-valuenow={fraction === null ? undefined : Math.round(fraction * 100)}
			data-testid={`job-progress-${job.id}`}
		>
			{fraction === null ? (
				<div className="bg-primary/70 absolute inset-y-0 left-0 w-1/3 animate-pulse rounded-full" />
			) : (
				<div
					className={cn(
						"h-full rounded-full transition-[width] duration-500 ease-(--ease-out)",
						job.status === "failed" ? "bg-chart-error" : job.status === "succeeded" ? "bg-chart-success" : "bg-primary",
					)}
					style={{ width: `${Math.max(2, fraction * 100)}%` }}
				/>
			)}
		</div>
	);
}

/** One job in the drawer: kind, target, status, progress and age, linking to its page. */
function JobRow({ job, onOpen }: { job: Job; onOpen: () => void }) {
	const kindLabel = useJobKindLabel();
	const { t } = useTranslation("platform");
	const when = formatRelative(job.finished_at ?? job.started_at ?? job.created_at, getLocale());
	return (
		<li>
			<Link
				to="/jobs/$jobId"
				params={{ jobId: job.id }}
				onClick={onOpen}
				className="group hover:bg-accent/70 focus-visible:bg-accent/70 flex flex-col gap-2 rounded-md px-3 py-2.5 transition-colors"
				data-testid={`jobs-drawer-row-${job.id}`}
			>
				<span className="flex items-center gap-2">
					<span className="text-foreground truncate text-sm font-medium">{jobKindLabelFor(job.kind, job.params, kindLabel(job.kind))}</span>
					<JobStatusBadge status={job.status} className="ml-auto" />
					<ChevronRight
						className="text-muted-foreground size-3.5 shrink-0 opacity-0 transition-opacity group-hover:opacity-100"
						aria-hidden="true"
					/>
				</span>
				<span className="text-muted-foreground flex items-center gap-2 text-xs">
					{job.target ? (
						<span className="truncate" title={job.target}>
							{targetLabel(job.target).text}
						</span>
					) : (
						<span className="truncate font-mono">{t("jobs.shortId", { id: shortJobId(job.id) })}</span>
					)}
					{job.cancel_requested && isActiveJob(job) && <span className="text-chart-warning-ink">{t("jobs.cancelRequested")}</span>}
					{when && <span className="ml-auto shrink-0 tabular-nums">{when}</span>}
				</span>
				{isActiveJob(job) && <JobProgressBar job={job} />}
				{job.status === "failed" && job.error && <span className="text-chart-error-ink line-clamp-2 text-xs">{job.error}</span>}
			</Link>
		</li>
	);
}

/**
 * Header button with the number of running jobs; opens the jobs drawer. Hidden when the server
 * has no jobs API (an older server answers 404), for demo visitors, and when the caller may not
 * read jobs.
 */
export function JobsIndicator() {
	const { t } = useTranslation("platform");
	const session = useSession();
	const [open, setOpen] = useState(false);
	const triggerRef = useRef<HTMLButtonElement>(null);
	const skip = (session.authMode === "hosted" && !session.signedIn) || session.status === "loading";
	const [busy, setBusy] = useState(false);
	const jobs = useListJobsQuery(undefined, {
		skip,
		pollingInterval: open || busy ? JOBS_POLL_MS : IDLE_POLL_MS,
		skipPollingIfUnfocused: true,
	});
	const list = jobs.data?.jobs ?? [];
	const running = list.filter(isActiveJob).length;
	useEffect(() => setBusy(running > 0), [running]);
	const kindLabel = useJobKindLabel();
	const { t: tt } = useTranslation("toast");

	// Finish notices: a toast with a link, and "(n done)" in the tab title until the drawer opens.
	const activeIds = useRef<Set<string> | null>(null);
	useEffect(() => {
		if (!jobs.data) return;
		const done = finishedSince(activeIds.current, jobs.data.jobs);
		activeIds.current = new Set(jobs.data.jobs.filter(isActiveJob).map((j) => j.id));
		if (done.length === 0) return;
		for (const job of done) {
			const name = jobKindLabelFor(job.kind, job.params, kindLabel(job.kind));
			notify({ kind: "info", text: tt("finished", { name }), action: { label: tt("viewResults"), to: `/jobs/${job.id}` } });
		}
		if (!open) addUnseenDone(done.length);
		// kindLabel and tt are stable enough; the effect runs per poll answer.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [jobs.data]);
	useEffect(() => {
		if (open) clearUnseenDone();
	}, [open]);

	const status = errorStatus(jobs.error);
	if (skip || (!jobs.data && (status === 404 || status === 401 || status === 403))) return null;

	return (
		<>
			<button
				ref={triggerRef}
				type="button"
				onClick={() => setOpen(true)}
				aria-haspopup="dialog"
				aria-expanded={open}
				aria-label={running > 0 ? t("jobs.indicatorRunning", { count: running }) : t("jobs.indicatorIdle")}
				title={running > 0 ? t("jobs.indicatorRunning", { count: running }) : t("jobs.indicatorIdle")}
				className={cn(
					"text-muted-foreground hover:bg-accent hover:text-accent-foreground relative flex h-8 cursor-pointer items-center gap-1.5 rounded-md px-2 text-xs font-medium transition-colors",
					running > 0 && "text-foreground",
				)}
				data-testid="jobs-indicator"
				data-running={running}
			>
				<ListTodo className="size-4" aria-hidden="true" />
				<span className="hidden sm:inline">{t("jobs.indicator")}</span>
				{running > 0 && (
					<span
						className="bg-primary text-primary-foreground flex h-4 min-w-4 items-center justify-center rounded-full px-1 font-mono text-[10px] leading-none font-semibold tabular-nums"
						data-testid="jobs-indicator-count"
					>
						{running}
					</span>
				)}
			</button>
			{open && (
				<JobsDrawer
					jobs={list}
					isLoading={jobs.isLoading}
					error={jobs.isError ? jobs.error : undefined}
					onRetry={() => void jobs.refetch()}
					onClose={() => {
						setOpen(false);
						triggerRef.current?.focus();
					}}
				/>
			)}
		</>
	);
}

export interface JobsDrawerProps {
	jobs: Job[];
	isLoading: boolean;
	error?: unknown;
	onRetry: () => void;
	onClose: () => void;
}

/** The jobs drawer: running and queued jobs first, then recent ones. A right-side shared Dialog (focus trapped, Escape or the scrim closes it). */
export function JobsDrawer({ jobs, isLoading, error, onRetry, onClose }: JobsDrawerProps) {
	const { t } = useTranslation("platform");
	const session = useSession();
	const { active, recent } = splitJobs(jobs);

	return (
		<Dialog
			open
			onOpenChange={(next) => !next && onClose()}
			side="right"
			title={t("jobs.drawerTitle")}
			description={<span className="text-xs">{session.org ? t("jobs.drawerSubtitle", { org: session.org.name }) : t("jobs.drawerSubtitleLocal")}</span>}
			closeLabel={t("jobs.close")}
			testId="jobs-drawer"
		>
			<div className="-mx-3 flex min-h-0 flex-1 flex-col gap-5 border-t pt-4">
				{isLoading ? (
					<BlockSkeleton rows={4} testId="jobs-drawer-loading" className="px-3" what={t("jobs.drawerTitle")} onRetry={onRetry} />
				) : error ? (
					<LoadError title={t("jobs.loadError")} error={error} onRetry={onRetry} testId="jobs-drawer-error" className="mx-3" />
				) : (
					<>
						<DrawerSection title={t("jobs.running")} count={active.length} empty={t("jobs.noneRunning")} testId="jobs-drawer-running">
							{active.map((job) => (
								<JobRow key={job.id} job={job} onOpen={onClose} />
							))}
						</DrawerSection>
						<DrawerSection title={t("jobs.recent")} count={recent.length} empty={t("jobs.noneRecent")} testId="jobs-drawer-recent">
							{recent.slice(0, RECENT_LIMIT).map((job) => (
								<JobRow key={job.id} job={job} onOpen={onClose} />
							))}
						</DrawerSection>
					</>
				)}
			</div>
		</Dialog>
	);
}

function DrawerSection({
	title,
	count,
	empty,
	testId,
	children,
}: {
	title: string;
	count: number;
	empty: string;
	testId: string;
	children: React.ReactNode;
}) {
	return (
		<section className="flex flex-col gap-1" data-testid={testId}>
			<h3 className="text-muted-foreground flex items-center gap-2 px-3 text-[11px] font-medium tracking-[0.06em] uppercase">
				{title}
				<span className="bg-muted text-muted-foreground rounded-full px-1.5 font-mono text-[10px] tabular-nums">{count}</span>
			</h3>
			{count === 0 ? <p className="text-muted-foreground px-3 py-2 text-sm">{empty}</p> : <ul className="flex flex-col">{children}</ul>}
		</section>
	);
}