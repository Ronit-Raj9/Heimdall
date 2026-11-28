import { JobLogPanel } from "@/components/jobs/jobLogPanel";
import { jobKindLabelFor, targetLabel } from "@/components/jobs/jobKindLabels";
import { useJobKindName } from "@/components/jobs/useJobKindName";
import { PageHeader } from "@/components/shell";
import { useEmbedded } from "@/components/shell/embedded";
import { RoleGate } from "@/components/platform/accessNotes";
import { ConfirmButton, JobStatusBadge } from "@/components/platform/badges";
import { useConfirm } from "@/components/platform/confirmDialog";
import { JobProgressBar } from "@/components/platform/jobsDrawer";
import { formatRelative, isActiveJob } from "@/components/platform/platform.utils";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Dialog } from "@/components/ui/dialog";
import { PaidButton } from "@/components/ui/paidButton";
import { canRetryFree } from "@/components/jobs/jobRetryCost";
import "@/components/experiments/ufk5I18n";
import { effectiveCap, useGetRunControlsQuery } from "@/lib/store/apis/runControlApi";
import { formatUsd } from "@/lib/money";
import "@/components/settings/ufrkI18n";
import { ShowTestsChip } from "@/components/ui/toolbar";
import { notify } from "@/lib/notify";
import { isTestObject, useShowTests, withoutTests } from "@/lib/testObjects";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import i18n, { getLocale } from "@/lib/i18n";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { useListJobsFilteredQuery } from "@/lib/store/apis/jobsApi";
import { useCancelJobMutation } from "@/lib/store/apis/platformApi";
import type { Job } from "@/lib/types/bench";
import { JOB_STATUSES } from "@/lib/types/jobs";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ListTodo, SquareTerminal } from "lucide-react";
import { parseAsString, useQueryStates } from "nuqs";
import { useEffect, useState, type ComponentProps } from "react";
import { useTranslation } from "react-i18next";
import { canRetryJob, firstErrorLine, isTestJob, jobBudgetUsd, jobsPollMs, parseJobStatus } from "./jobsView.utils";
import "@/components/platform/ufintI18n";

/** Jobs shown per step of "Load older jobs". */
export const JOBS_PAGE_SIZE = 50;

// Radix Select has no empty value, so "any" stands for no filter.
const ANY = "any";

const filterParsers = { kind: parseAsString.withDefault(""), status: parseAsString.withDefault("") };

/** The error of the last action, with the job it was for (N2-1). */
interface ActionError {
	jobTitle: string;
	error: unknown;
}

/** Every job of the organization: filter by kind and status, cancel what runs, retry what ended. */
export default function JobsView() {
	const { t } = useTranslation("jobs");
	const { t: tp } = useTranslation("platform");
	const kindName = useJobKindName();
	const embedded = useEmbedded();
	const confirm = useConfirm();
	const [showTests, setShowTests] = useShowTests();
	const [filters, setFilters] = useQueryStates(filterParsers, { history: "replace" });
	const kind = filters.kind;
	const status = parseJobStatus(filters.status, JOB_STATUSES);
	const [limit, setLimit] = useState(JOBS_PAGE_SIZE);
	// Fast polling only while a listed job is active (M1-62).
	const [anyActive, setAnyActive] = useState(true);
	const list = useListJobsFilteredQuery(
		{ kind: kind || undefined, status: status || undefined, limit },
		{ pollingInterval: jobsPollMs(anyActive), skipPollingIfUnfocused: true },
	);
	useEffect(() => {
		if (list.data) setAnyActive(list.data.jobs.some(isActiveJob));
	}, [list.data]);
	const [cancel, cancelState] = useCancelJobMutation();
	const [retry, retryState] = useCreateJobMutation();
	const [retried, setRetried] = useState<string | null>(null);
	const [logId, setLogId] = useState<string | null>(null);
	const [actionError, setActionError] = useState<ActionError | null>(null);
	const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
	const filtered = kind !== "" || status !== "";
	// M1-18: jobs of test objects (zz-test- benchmarks and agents) are hidden unless asked for.
	const { shown: jobs, hidden: hiddenTests } = withoutTests(list.data?.jobs ?? [], (j) => isTestJob(j, isTestObject), showTests);
	const kinds = [...new Set([...(list.data?.kinds ?? []), ...(kind ? [kind] : [])])].sort();
	const title = (job: Job) => jobTitle(job, kindName, (k, target) => t("jobOf", { kind: k, target }));

	const doCancel = async (job: Job) => {
		setActionError(null);
		const res = await cancel(job.id);
		if ("error" in res && res.error !== undefined) setActionError({ jobTitle: title(job), error: res.error });
		else notify({ text: t("stopped", { ns: "toast", name: title(job) }) });
	};

	// M2-1, M1-51: Retry calls models again, so the shared PaidButton asks first (kind="retry")
	// and names the spend cap when the job has one; this only sends the job.
	const doRetry = async (job: Job) => {
		setRetried(null);
		setActionError(null);
		const res = await retry({ kind: job.kind, params: job.params ?? {} });
		if ("data" in res && res.data) {
			setRetried(res.data.id);
			notify({ text: t("started", { ns: "toast", name: title(job) }), action: { label: t("openNew"), to: `/jobs/${res.data.id}` } });
			void list.refetch();
		} else if ("error" in res) {
			setActionError({ jobTitle: title(job), error: res.error });
		}
	};

	// M2-7: cancel several running jobs at once, after one confirm.
	const cancelSelected = async () => {
		const picked = jobs.filter((j) => selected.has(j.id) && isActiveJob(j) && !j.cancel_requested);
		if (picked.length === 0) return;
		if (
			!(await confirm(t("bulk.confirm", { count: picked.length }), {
				title: i18n.t("confirm.cancelJobs", { ns: "ufint", count: picked.length }),
				confirmLabel: t("confirm"),
				cancelLabel: t("keep"),
				destructive: true,
			}))
		)
			return;
		for (const job of picked) await doCancel(job);
		setSelected(new Set());
	};

	const toggle = (id: string, on: boolean) =>
		setSelected((prev) => {
			const next = new Set(prev);
			if (on) next.add(id);
			else next.delete(id);
			return next;
		});

	return (
		<section className={cn("flex w-full min-w-0 flex-col gap-6", !embedded && "p-4 sm:p-6")} data-testid="jobs-screen">
			<PageHeader title={t("title")} />

			<div className="flex flex-wrap items-end gap-3" data-testid="jobs-filters">
				<div className="flex flex-col gap-1 text-xs">
					<span className="text-muted-foreground font-medium" id="jobs-filter-kind-label">
						{t("filters.kind")}
					</span>
					<Select value={kind || ANY} onValueChange={(v) => void setFilters({ kind: v === ANY ? "" : v })}>
						<SelectTrigger size="sm" className="min-w-40" aria-labelledby="jobs-filter-kind-label" data-testid="jobs-filter-kind">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							<SelectItem value={ANY}>{t("filters.anyKind")}</SelectItem>
							{kinds.map((k) => (
								<SelectItem key={k} value={k}>
									{kindName(k)}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
				<div className="flex flex-col gap-1 text-xs">
					<span className="text-muted-foreground font-medium" id="jobs-filter-status-label">
						{t("filters.status")}
					</span>
					<Select value={status || ANY} onValueChange={(v) => void setFilters({ status: v === ANY ? "" : v })}>
						<SelectTrigger size="sm" className="min-w-36" aria-labelledby="jobs-filter-status-label" data-testid="jobs-filter-status">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							<SelectItem value={ANY}>{t("filters.anyStatus")}</SelectItem>
							{JOB_STATUSES.map((s) => (
								<SelectItem key={s} value={s}>
									{tp(`jobs.statuses.${s}`)}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
				<ShowTestsChip count={hiddenTests} on={showTests} onChange={setShowTests} />
				{filtered && (
					<Button variant="ghost" size="sm" onClick={() => void setFilters({ kind: "", status: "" })} dataTestId="jobs-filter-clear">
						{t("filters.clear")}
					</Button>
				)}
				{selected.size > 0 && (
					<RoleGate min="member" fallback={null}>
						<div className="ml-auto flex items-center gap-2" data-testid="jobs-bulk-bar">
							<span className="text-muted-foreground text-sm">{t("bulk.selected", { count: selected.size })}</span>
							<Button
								variant="destructive"
								size="sm"
								disabled={cancelState.isLoading}
								onClick={() => void cancelSelected()}
								dataTestId="jobs-bulk-cancel"
							>
								{t("bulk.cancel")}
							</Button>
							<Button variant="ghost" size="sm" onClick={() => setSelected(new Set())} dataTestId="jobs-bulk-clear">
								{t("bulk.clear")}
							</Button>
						</div>
					</RoleGate>
				)}
			</div>

			{actionError && (
				<p role="alert" className="text-danger-ink text-sm" data-testid="jobs-action-error">
					{t("actionErrorFor", { job: actionError.jobTitle, message: getBenchErrorMessage(actionError.error) })}
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
				jobs={list.data ? jobs : undefined}
				isLoading={list.isLoading}
				error={list.isError ? list.error : undefined}
				onRetryLoad={() => void list.refetch()}
				filtered={filtered}
				kindName={kindName}
				onCancel={(job) => void doCancel(job)}
				onRetry={doRetry}
				busy={cancelState.isLoading || retryState.isLoading}
				onOpenLog={(job) => setLogId(job.id)}
				openLogId={logId}
				selected={selected}
				onSelect={toggle}
			/>

			{list.data?.next && (
				<div>
					<Button
						variant="outline"
						size="sm"
						onClick={() => setLimit((n) => Math.min(500, n + JOBS_PAGE_SIZE))}
						dataTestId="jobs-load-more"
					>
						{t("loadMore")}
					</Button>
				</div>
			)}
			{logId && <JobLogDrawer jobId={logId} onClose={() => setLogId(null)} />}
		</section>
	);
}

/** A job's list title: the kind label, then the comparison name or target without its prefix (M8-58). */
export function jobTitle(job: Job, kindName: (kind: string) => string, of: (kind: string, target: string) => string): string {
	const kindLabel = jobKindLabelFor(job.kind, job.params, kindName(job.kind));
	const name = typeof job.params?.name === "string" && job.params.name ? job.params.name : targetLabel(job.target);
	return name ? of(kindLabel, name) : kindLabel;
}

/** The live log of one job in the shared right-side Dialog: focus moves in, Escape or the backdrop closes it, focus goes back after (M1-37). */
export function JobLogDrawer({ jobId, onClose }: { jobId: string; onClose: () => void }) {
	const { t } = useTranslation("jobs");
	return (
		<Dialog
			open
			onOpenChange={(open) => !open && onClose()}
			title={t("drawer.title")}
			closeLabel={t("panel.close")}
			side="right"
			size="lg"
			hideClose
			testId="jobs-log-drawer"
		>
			<JobLogPanel jobId={jobId} onClose={onClose} />
		</Dialog>
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
	onRetry: (job: Job) => Promise<unknown>;
	busy: boolean;
	onOpenLog?: (job: Job) => void;
	openLogId?: string | null;
	/** Running jobs picked for "Cancel selected" (M2-7). */
	selected?: ReadonlySet<string>;
	onSelect?: (id: string, on: boolean) => void;
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
	selected,
	onSelect,
}: JobsTableProps) {
	const { t } = useTranslation("jobs");
	if (isLoading) return <BlockSkeleton rows={6} testId="jobs-loading" />;
	if (error !== undefined) return <LoadError title={t("loadError")} error={error} onRetry={onRetryLoad} testId="jobs-error" />;
	if (!jobs || jobs.length === 0) {
		return filtered ? (
			<EmptyState icon={ListTodo} message={t("emptyFiltered")} testId="jobs-empty-filtered" />
		) : (
			<EmptyState icon={ListTodo} message={t("empty")} testId="jobs-empty" />
		);
	}
	const locale = getLocale();
	const title = (job: Job) => jobTitle(job, kindName, (k, target) => t("jobOf", { kind: k, target }));
	const actions = (job: Job, prefix: string) => (
		<JobActions job={job} busy={busy} onCancel={onCancel} onRetry={onRetry} onOpenLog={onOpenLog} idPrefix={prefix} title={title(job)} />
	);
	const check = (job: Job, prefix: string) =>
		onSelect && isActiveJob(job) && !job.cancel_requested ? (
			<RoleGate min="member" fallback={null}>
				<input
					type="checkbox"
					className="relative z-10 size-4"
					checked={selected?.has(job.id) ?? false}
					onChange={(e) => onSelect(job.id, e.target.checked)}
					aria-label={t("bulk.pick", { job: title(job) })}
					data-testid={`${prefix}-pick-${job.id}`}
				/>
			</RoleGate>
		) : null;
	return (
		<>
			{/* Phones: one block per job, so status and actions never sit off screen (M2-2, N8-1). */}
			<ul className="bg-card divide-y rounded-xl border md:hidden" data-testid="jobs-list-mobile">
				{jobs.map((job) => (
					<li key={job.id} className={cn("relative flex flex-col gap-2 p-3", openLogId === job.id && "bg-accent/50")}>
						<div className="flex items-start gap-2">
							{check(job, "jobs-m")}
							<Link
								to="/jobs/$jobId"
								params={{ jobId: job.id }}
								className="text-foreground min-w-0 flex-1 font-medium break-words after:absolute after:inset-0 hover:underline"
							>
								{title(job)}
							</Link>
						</div>
						<div className="flex flex-wrap items-center gap-2">
							<JobStatusBadge status={job.status} />
							<span className="text-muted-foreground text-xs">{formatRelative(job.created_at, locale) ?? "-"}</span>
						</div>
						{isActiveJob(job) && <JobProgressBar job={job} className="w-full" />}
						<JobReason job={job} />
						<div className="relative z-10 flex flex-wrap gap-2">{actions(job, "jobs-m")}</div>
					</li>
				))}
			</ul>
			<div className="bg-card hidden min-w-0 overflow-x-auto rounded-xl border md:block">
				<table className="w-full text-sm" data-testid="jobs-table">
					<thead className="bg-muted/40 text-muted-foreground sticky top-0 text-left text-xs">
						<tr className="border-b">
							{onSelect && (
								<th className="w-8 px-3 py-2.5">
									<span className="sr-only">{t("bulk.column")}</span>
								</th>
							)}
							<th className="px-4 py-2.5 font-medium">{t("columns.kind")}</th>
							<th className="px-4 py-2.5 font-medium">{t("columns.status")}</th>
							<th className="min-w-28 px-4 py-2.5 font-medium whitespace-nowrap">{t("columns.created")}</th>
							<th className="px-4 py-2.5 text-right font-medium">{t("columns.actions")}</th>
						</tr>
					</thead>
					<tbody>
						{jobs.map((job) => {
							const active = isActiveJob(job);
							return (
								<tr
									key={job.id}
									className={cn(
										"hover:bg-accent/40 relative border-b transition-colors last:border-b-0",
										openLogId === job.id && "bg-accent/50",
									)}
									data-testid={`jobs-row-${job.id}`}
								>
									{onSelect && <td className="px-3 py-2">{check(job, "jobs")}</td>}
									<td className="w-full max-w-0 px-4 py-2">
										<Link
											to="/jobs/$jobId"
											params={{ jobId: job.id }}
											title={job.id}
											className="text-foreground block truncate font-medium after:absolute after:inset-0 hover:underline"
										>
											{title(job)}
										</Link>
									</td>
									<td className="px-4 py-2 whitespace-nowrap">
										<div className="flex flex-col gap-1.5">
											<JobStatusBadge status={job.status} />
											{active && <JobProgressBar job={job} className="w-28" />}
											<JobReason job={job} />
										</div>
									</td>
									<td className="text-muted-foreground min-w-28 px-4 py-2 text-xs whitespace-nowrap">
										{formatRelative(job.created_at, locale) ?? "-"}
									</td>
									<td className="relative z-10 px-4 py-2 text-right whitespace-nowrap">
										<div className="flex items-center justify-end gap-1.5">{actions(job, "jobs")}</div>
									</td>
								</tr>
							);
						})}
					</tbody>
				</table>
			</div>
		</>
	);
}

/** One line of why a job failed, under its badge (M2-3, N8-1). */
function JobReason({ job }: { job: Job }) {
	const line = job.status === "failed" ? firstErrorLine(job.error) : "";
	if (!line) return null;
	return (
		<p className="text-danger-ink line-clamp-2 max-w-64 text-xs" title={job.error} data-testid={`jobs-reason-${job.id}`}>
			{line}
		</p>
	);
}

function JobActions({
	job,
	busy,
	onCancel,
	onRetry,
	onOpenLog,
	idPrefix,
	title,
}: {
	job: Job;
	/** The job's list title, named in the Retry confirm. */
	title: string;
	busy: boolean;
	/** "jobs" in the table, "jobs-m" in the phone list, so test ids stay unique on the page. */
	idPrefix: string;
	onCancel: (job: Job) => void;
	onRetry: (job: Job) => Promise<unknown>;
	onOpenLog?: (job: Job) => void;
}) {
	const { t } = useTranslation("jobs");
	const { t: tr } = useTranslation("ufrk");
	const active = isActiveJob(job);
	return (
		<>
			{onOpenLog && (
				<Button
					variant="ghost"
					size="sm"
					className="h-7 gap-1 px-2 text-xs"
					onClick={() => onOpenLog(job)}
					dataTestId={`${idPrefix}-log-${job.id}`}
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
						testId={`${idPrefix}-cancel-${job.id}`}
					/>
				) : canRetryJob(job) && canRetryFree(job) ? (
					// R3-4: a replay calls no model, so its Retry says free and asks nothing.
					<Button
						type="button"
						variant="outline"
						size="sm"
						className="h-7 gap-1 px-2.5 text-xs"
						disabled={busy}
						title={tr("jobs.retryFreeTitle")}
						onClick={() => void onRetry(job)}
						data-testid={`${idPrefix}-retry-${job.id}`}
					>
						{tr("jobs.retryFree")}
					</Button>
				) : canRetryJob(job) ? (
					<RetryPaidButton
						job={job}
						verb={t("retry")}
						noBudgetText={t("retryConfirmNoBudget", { kind: title })}
						estimateAt="title"
						variant="outline"
						size="sm"
						className="h-7 gap-1 px-2.5 text-xs"
						disabled={busy}
						onConfirm={() => onRetry(job)}
						testId={`${idPrefix}-retry-${job.id}`}
					/>
				) : null}
			</RoleGate>
		</>
	);
}

/** The experiment a comparison run job runs, from its params or target; "" for other kinds. */
export function retryExpId(job: Pick<Job, "kind" | "params" | "target">): string {
	if (job.kind !== "experiment.run") return "";
	const raw = typeof job.params?.exp_id === "string" ? job.params.exp_id : (job.target ?? "");
	return raw.replace(/^experiment:/, "");
}

/**
 * Retry of a paid job (K5, PQ1-7): the job's own budget, else for a comparison run job the
 * comparison's cap in effect (the live override, else the frozen one), which the scheduler enforces
 * on the rerun because it runs the same frozen manifest.
 */
function RetryPaidButton({
	job,
	verb,
	noBudgetText,
	...rest
}: { job: Job; verb: string; noBudgetText: string } & Omit<ComponentProps<typeof PaidButton>, "verb" | "capUsd" | "kind" | "confirm">) {
	const { t: tk } = useTranslation("ufk5");
	const expId = retryExpId(job);
	const controls = useGetRunControlsQuery(expId, { skip: expId === "" || jobBudgetUsd(job) !== null });
	const cap = jobBudgetUsd(job) ?? (controls.data ? effectiveCap(controls.data) || null : null);
	return (
		<PaidButton
			{...rest}
			verb={verb}
			capUsd={cap}
			confirm="always"
			kind="retry"
			confirmText={cap !== null ? tk("retry.capHelp", { cap: formatUsd(cap) }) : noBudgetText}
		/>
	);
}