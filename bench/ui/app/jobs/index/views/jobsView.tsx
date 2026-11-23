import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { jobKindLabelFor } from "@/components/jobs/jobKindLabels";
import { useJobKindName } from "@/components/jobs/useJobKindName";
import { PageHeader } from "@/components/shell";
import { RoleGate } from "@/components/platform/accessNotes";
import { ConfirmButton, JobStatusBadge } from "@/components/platform/badges";
import { JobProgressBar } from "@/components/platform/jobsDrawer";
import { formatRelative, isActiveJob } from "@/components/platform/platform.utils";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { useListJobsFilteredQuery } from "@/lib/store/apis/jobsApi";
import { JOBS_POLL_MS, useCancelJobMutation } from "@/lib/store/apis/platformApi";
import type { Job, JobStatus } from "@/lib/types/bench";
import { JOB_STATUSES } from "@/lib/types/jobs";
import { Link } from "@tanstack/react-router";
import { ListTodo, RotateCcw, SquareTerminal } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/** Jobs shown per step of "Load older jobs". */
export const JOBS_PAGE_SIZE = 50;

const SELECT_CLASS =
	"border-input bg-background text-foreground h-8 rounded-md border px-2 text-sm focus-visible:ring-ring/50 focus-visible:ring-2 focus-visible:outline-none";

/** Every job of the organization: filter by kind and status, cancel what runs, retry what ended. */
export default function JobsView() {
	const { t } = useTranslation("jobs");
	const { t: tp } = useTranslation("platform");
	const kindName = useJobKindName();
	const [kind, setKind] = useState("");
	const [status, setStatus] = useState<JobStatus | "">("");
	const [limit, setLimit] = useState(JOBS_PAGE_SIZE);
	const list = useListJobsFilteredQuery(
		{ kind: kind || undefined, status: status || undefined, limit },
		{ pollingInterval: JOBS_POLL_MS, skipPollingIfUnfocused: true },
	);
	const [cancel, cancelState] = useCancelJobMutation();
	const [retry, retryState] = useCreateJobMutation();
	const [retried, setRetried] = useState<string | null>(null);
	const [logId, setLogId] = useState<string | null>(null);
	const filtered = kind !== "" || status !== "";
	const kinds = [...new Set([...(list.data?.kinds ?? []), ...(kind ? [kind] : [])])].sort();
	const actionError = cancelState.error ?? retryState.error;

	const doRetry = async (job: Job) => {
		setRetried(null);
		const res = await retry({ kind: job.kind, params: job.params ?? {} });
		if ("data" in res && res.data) {
			setRetried(res.data.id);
			void list.refetch();
		}
	};

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-6 p-4 sm:p-6" data-testid="jobs-screen">
			<PageHeader title={t("title")} subtitle={t("subtitle")} />

			<div className="flex flex-wrap items-end gap-3" data-testid="jobs-filters">
				<label className="flex flex-col gap-1 text-xs">
					<span className="text-muted-foreground font-medium">{t("filters.kind")}</span>
					<select className={SELECT_CLASS} value={kind} onChange={(e) => setKind(e.target.value)} data-testid="jobs-filter-kind">
						<option value="">{t("filters.anyKind")}</option>
						{kinds.map((k) => (
							<option key={k} value={k}>
								{kindName(k)}
							</option>
						))}
					</select>
				</label>
				<label className="flex flex-col gap-1 text-xs">
					<span className="text-muted-foreground font-medium">{t("filters.status")}</span>
					<select
						className={SELECT_CLASS}
						value={status}
						onChange={(e) => setStatus(e.target.value as JobStatus | "")}
						data-testid="jobs-filter-status"
					>
						<option value="">{t("filters.anyStatus")}</option>
						{JOB_STATUSES.map((s) => (
							<option key={s} value={s}>
								{tp(`jobs.statuses.${s}`)}
							</option>
						))}
					</select>
				</label>
				{filtered && (
					<Button
						variant="ghost"
						size="sm"
						onClick={() => {
							setKind("");
							setStatus("");
						}}
						dataTestId="jobs-filter-clear"
					>
						{t("filters.clear")}
					</Button>
				)}
			</div>

			{actionError !== undefined && (
				<p role="alert" className="text-chart-error-ink text-sm" data-testid="jobs-action-error">
					{t("actionError")}: {getBenchErrorMessage(actionError)}
				</p>
			)}
			{retried && (
				<p className="text-foreground text-sm" data-testid="jobs-retried">
					{t("retried")}{" "}
					<Link to="/jobs/$jobId" params={{ jobId: retried }} className="underline">
						{t("openNew")}
					</Link>
				</p>
			)}

			<JobsTable
				jobs={list.data?.jobs}
				isLoading={list.isLoading}
				error={list.isError ? list.error : undefined}
				onRetryLoad={() => void list.refetch()}
				filtered={filtered}
				kindName={kindName}
				onCancel={(job) => void cancel(job.id)}
				onRetry={(job) => void doRetry(job)}
				busy={cancelState.isLoading || retryState.isLoading}
				onOpenLog={(job) => setLogId(job.id)}
				openLogId={logId}
			/>

			{list.data?.next && (
				<div>
					<Button variant="outline" size="sm" onClick={() => setLimit((n) => Math.min(500, n + JOBS_PAGE_SIZE))} dataTestId="jobs-load-more">
						{t("loadMore")}
					</Button>
				</div>
			)}
			{logId && <JobLogDrawer jobId={logId} onClose={() => setLogId(null)} />}
		</section>
	);
}

/** The live log of one job in a panel that slides in from the right; Escape or the backdrop closes it. */
export function JobLogDrawer({ jobId, onClose }: { jobId: string; onClose: () => void }) {
	const { t } = useTranslation("jobs");
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose]);
	return (
		<div className="fixed inset-0 z-50 flex justify-end" data-testid="jobs-log-drawer">
			<button type="button" aria-label={t("panel.close")} className="absolute inset-0 bg-black/40 backdrop-blur-[1px]" onClick={onClose} />
			<aside
				role="dialog"
				aria-modal="true"
				aria-label={t("drawer.title")}
				className="bg-background animate-in slide-in-from-right relative flex h-full w-full max-w-xl flex-col overflow-y-auto border-l p-4 shadow-2xl sm:p-5"
			>
				<JobLogPanel jobId={jobId} onClose={onClose} />
			</aside>
		</div>
	);
}

export interface JobsTableProps {
	jobs: Job[] | undefined;
	isLoading: boolean;
	error: unknown;
	onRetryLoad: () => void;
	filtered: boolean;
	kindName: (kind: string) => string;
	onCancel: (job: Job) => void;
	onRetry: (job: Job) => void;
	busy: boolean;
	onOpenLog?: (job: Job) => void;
	openLogId?: string | null;
}

export function JobsTable({
	jobs,
	isLoading,
	error,
	onRetryLoad,
	filtered,
	kindName,
	onCancel,
	onRetry,
	busy,
	onOpenLog,
	openLogId,
}: JobsTableProps) {
	const { t } = useTranslation("jobs");
	if (isLoading) return <BlockSkeleton rows={6} testId="jobs-loading" />;
	if (error !== undefined) return <LoadError title={t("loadError")} error={error} onRetry={onRetryLoad} testId="jobs-error" />;
	if (!jobs || jobs.length === 0) {
		return filtered ? (
			<EmptyState icon={ListTodo} message={t("emptyFiltered")} testId="jobs-empty-filtered" />
		) : (
			<EmptyState icon={ListTodo} message={t("empty")} action={{ label: t("emptyAction"), to: "/catalog" }} testId="jobs-empty" />
		);
	}
	const locale = getLocale();
	return (
		<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
			<table className="w-full text-sm" data-testid="jobs-table">
				<thead className="bg-muted/40 text-muted-foreground sticky top-0 text-left text-xs">
					<tr className="border-b">
						<th className="px-4 py-2.5 font-medium">{t("columns.kind")}</th>
						<th className="px-4 py-2.5 font-medium">{t("columns.target")}</th>
						<th className="px-4 py-2.5 font-medium">{t("columns.status")}</th>
						<th className="px-4 py-2.5 font-medium">{t("columns.created")}</th>
						<th className="px-4 py-2.5 text-right font-medium">{t("columns.actions")}</th>
					</tr>
				</thead>
				<tbody>
					{jobs.map((job) => {
						const active = isActiveJob(job);
						return (
							<tr
								key={job.id}
								className={`hover:bg-accent/40 border-b transition-colors last:border-b-0 ${openLogId === job.id ? "bg-accent/50" : ""}`}
								data-testid={`jobs-row-${job.id}`}
							>
								<td className="px-4 py-3">
									<Link to="/jobs/$jobId" params={{ jobId: job.id }} className="text-foreground font-medium hover:underline">
										{job.target
											? t("jobOf", { kind: jobKindLabelFor(job.kind, job.params, kindName(job.kind)), target: job.target })
											: jobKindLabelFor(job.kind, job.params, kindName(job.kind))}
									</Link>
									<div className="text-muted-foreground/60 max-w-48 truncate font-mono text-[10px]" title={job.id}>
										{job.id}
									</div>
								</td>
								<td className="text-muted-foreground max-w-56 truncate px-4 py-3 text-xs" title={job.target || undefined}>
									{job.target || "-"}
								</td>
								<td className="px-4 py-3">
									<div className="flex flex-col gap-1.5">
										<JobStatusBadge status={job.status} />
										{active && <JobProgressBar job={job} className="w-28" />}
									</div>
								</td>
								<td className="text-muted-foreground px-4 py-3 text-xs whitespace-nowrap">{formatRelative(job.created_at, locale) ?? "-"}</td>
								<td className="px-4 py-3 text-right">
									<div className="flex items-center justify-end gap-1.5">
									{onOpenLog && (
										<Button
											variant="ghost"
											size="sm"
											className="h-7 gap-1 px-2 text-xs"
											onClick={() => onOpenLog(job)}
											dataTestId={`jobs-log-${job.id}`}
										>
											<SquareTerminal className="size-3.5" aria-hidden="true" />
											{t("drawer.open")}
										</Button>
									)}
									<RoleGate min="member" fallback={null}>
										{active && !job.cancel_requested ? (
											<ConfirmButton
												label={t("cancel")}
												question={t("confirmCancel")}
												confirmLabel={t("confirm")}
												cancelLabel={t("keep")}
												onConfirm={() => onCancel(job)}
												disabled={busy}
												variant="destructive"
												testId={`jobs-cancel-${job.id}`}
											/>
										) : !active ? (
											<Button
												variant="outline"
												size="sm"
												className="h-7 gap-1 px-2.5 text-xs"
												title={t("retryTitle")}
												disabled={busy}
												onClick={() => onRetry(job)}
												dataTestId={`jobs-retry-${job.id}`}
											>
												<RotateCcw className="size-3.5" aria-hidden="true" />
												{t("retry")}
											</Button>
										) : null}
									</RoleGate>
									</div>
								</td>
							</tr>
						);
					})}
				</tbody>
			</table>
		</div>
	);
}
