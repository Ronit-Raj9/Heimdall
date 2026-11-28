import "./w449I18n";

import { flipDate } from "@/components/validity/flipBanner";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { decisionTone, TONE_TEXT } from "@/components/verdict/verdict.utils";
import { plainJobError } from "@/components/findings/jobModel";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { useGetJobQuery } from "@/lib/store/apis/platformApi";
import { useListExperimentsQuery } from "@/lib/store";
import { type ValidityEntry, type ValidityStale, useGetValidityQuery } from "@/lib/store/apis/validityApi";
import { useSession } from "@/lib/session/sessionContext";
import type { JobStatus } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { notify } from "@/lib/notify";
import { EmptyState } from "@/components/shell/emptyState";
import { Link } from "@tanstack/react-router";
import { ArrowRight } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/** How often a running scan job is polled. */
const SCAN_POLL_MS = 3000;
const ENDED: ReadonlySet<JobStatus> = new Set(["succeeded", "failed", "cancelled"]);

/** The rows a reader has not dismissed are marked "New". */
export function isUnseen(e: ValidityEntry, user: string | undefined): boolean {
	return e.flipped && !(user && e.seen_by?.includes(user));
}

/** The newest time any comparison was checked by a scan, or null when none was. */
export function lastScanAt(stale: readonly ValidityStale[]): string | null {
	let best: string | null = null;
	for (const s of stale) if (s.checked && (best === null || s.checked > best)) best = s.checked;
	return best;
}

/** Comparison names by id (Q-78): rows show the name, the id under it. Pure. */
export function comparisonNames(experiments: readonly { exp_id: string; name?: string }[] | undefined): Map<string, string> {
	const out = new Map<string, string>();
	for (const e of experiments ?? []) if (e.name && e.name !== e.exp_id) out.set(e.exp_id, e.name);
	return out;
}

/** A comparison as a link: its name when known, the id under it; the id alone otherwise. */
function ComparisonLink({ id, names }: { id: string; names: Map<string, string> }) {
	const name = names.get(id);
	return (
		<span className="flex max-w-80 min-w-0 flex-col">
			<Link to="/experiments/$expId" params={{ expId: id }} className="text-primary truncate hover:underline" title={name ?? id}>
				{name ?? id}
			</Link>
			{name && (
				<span className="text-muted-foreground truncate font-mono text-xs" title={id}>
					{id}
				</span>
			)}
		</span>
	);
}

function ChangedTable({ entries, user, names }: { entries: ValidityEntry[]; user?: string; names: Map<string, string> }) {
	const { t } = useTranslation("w449");
	const { t: tg } = useTranslation("glossary");
	const word = (d: string) => tg(`terms.${d}.label`, { defaultValue: d });
	if (entries.length === 0) {
		return <EmptyState compact body={t("page.empty")} testId="validity-empty" />;
	}
	return (
		<Table data-testid="validity-table" stickyColumns={1}>
			<TableHeader>
				<TableRow>
					<TableHead>{t("page.comparison")}</TableHead>
					<TableHead>{t("page.rescoredOn")}</TableHead>
					<TableHead>{t("page.wasIsNow")}</TableHead>
					<TableHead>{t("page.why")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{entries.map((e) => (
					<TableRow key={e.id} data-testid="validity-row">
						<TableCell>
							<div className="flex min-w-0 items-center gap-2">
								<ComparisonLink id={e.exp_id} names={names} />
								{isUnseen(e, user) && (
									<Badge variant="info" data-testid="validity-new">
										{t("page.new")}
									</Badge>
								)}
							</div>
						</TableCell>
						<TableCell className="tabular-nums">{flipDate(e.at)}</TableCell>
						<TableCell>
							<span className="inline-flex items-center gap-1.5">
								<span className={TONE_TEXT[decisionTone(e.before.decision)]}>{word(e.before.decision)}</span>
								<ArrowRight className="text-muted-foreground size-3.5" aria-label={t("page.became")} />
								<span className={cn("font-medium", TONE_TEXT[decisionTone(e.after.decision)])}>{word(e.after.decision)}</span>
							</span>
						</TableCell>
						<TableCell className="text-muted-foreground min-w-56 whitespace-normal">{e.note ?? ""}</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function StaleList({ stale, names }: { stale: ValidityStale[]; names: Map<string, string> }) {
	const { t } = useTranslation("w449");
	if (stale.length === 0) return <EmptyState compact body={t("page.noneStale")} testId="validity-none-stale" />;
	return (
		<ul className="divide-border flex flex-col divide-y text-sm" data-testid="validity-stale">
			{stale.map((s) => (
				<li key={s.exp_id} className="flex min-h-8 flex-wrap items-center gap-x-2 px-3 py-2">
					<ComparisonLink id={s.exp_id} names={names} />
					<span className="text-muted-foreground">
						{t("page.updatedChecks", {
							gates: Object.keys(s.gates).join(", "),
						})}
					</span>
				</li>
			))}
		</ul>
	);
}

/**
 * K4: a failed scan as LoadError reads it: an object with the server's sentence as a string
 * `error`, without the job prefixes. A plain Error was read as "Unexpected error".
 */
export function scanFailure(message: string): { error: string } {
	return { error: plainJobError(message) };
}

/** W4-49: "Changed results", the validity ledger of the org. */
export default function ValidityView() {
	const { t } = useTranslation("w449");
	const session = useSession();
	const validity = useGetValidityQuery();
	const names = comparisonNames(useListExperimentsQuery().data?.experiments);
	const data = validity.data;
	const [createJob, create] = useCreateJobMutation();
	const [jobId, setJobId] = useState<string | null>(null);
	// N4-1: the caught error of a failed scan (create refused or the job failed), shown as a LoadError.
	const [scanError, setScanError] = useState<unknown>(null);
	// The scan runs as a job: poll it, and reload the lists when it ends.
	const job = useGetJobQuery(jobId ?? "", {
		skip: !jobId,
		pollingInterval: SCAN_POLL_MS,
		skipPollingIfUnfocused: true,
	});
	const status = job.data && job.data.id === jobId ? job.data.status : undefined;
	const ended = status ? ENDED.has(status) : false;
	const { refetch } = validity;
	useEffect(() => {
		if (!ended) return;
		if (status !== "succeeded") setScanError(scanFailure(job.data?.error || t("page.scanFailed")));
		else notify({ text: t("page.scanDone") });
		setJobId(null);
		void refetch();
	}, [ended, status, job.data?.error, refetch, t]);
	const scanning = Boolean(jobId) || create.isLoading;
	const user = session.user?.id ?? "local";
	const scan = async () => {
		setScanError(null);
		try {
			setJobId((await createJob({ kind: "validity.scan" }).unwrap()).id);
		} catch (error) {
			setScanError(scanFailure(getBenchErrorMessage(error)));
		}
	};
	const last = lastScanAt(data?.stale ?? []);
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="validity-screen">
			<PageHeader title={t("page.heading")} />
			{validity.isLoading ? (
				<BlockSkeleton rows={4} testId="validity-loading" />
			) : validity.isError || !data ? (
				<LoadError title={t("page.loadError")} error={validity.error} onRetry={() => void refetch()} testId="validity-error" />
			) : (
				<>
					<section className="flex flex-col gap-2" data-testid="validity-changed">
						<h2 className="text-base font-semibold">{t("page.changedHeading")}</h2>
						<div className="bg-card min-w-0 overflow-hidden rounded-xl border">
							<ChangedTable entries={data.entries} user={user} names={names} />
						</div>
					</section>
					<section className="flex flex-col gap-2" data-testid="validity-could-change">
						<h2 className="text-base font-semibold">{t("page.couldChange")}</h2>
						<div className="bg-card min-w-0 rounded-xl border">
							<StaleList stale={data.stale} names={names} />
						</div>
						<div className="flex flex-col gap-1">
							<div className="flex flex-wrap items-center gap-3">
								<Button
									size="sm"
									variant="outline"
									className="h-auto w-full whitespace-normal sm:w-auto"
									disabled={scanning}
									onClick={() => void scan()}
									data-testid="validity-scan"
								>
									{scanning ? t("page.scanning") : t("page.scan")}
								</Button>
								{scanning && (
									<Link to="/jobs" className="text-primary text-sm hover:underline" data-testid="validity-scan-jobs">
										{t("page.seeJobs")}
									</Link>
								)}
							</div>
							<p className="text-muted-foreground text-xs">
								{t("page.scanHelp")}
								{last ? ` ${t("page.lastScan", { date: flipDate(last) })}` : ""}
							</p>
							{scanError !== null && (
								<LoadError title={t("page.scanFailed")} error={scanError} onRetry={() => void scan()} testId="validity-scan-error" />
							)}
						</div>
					</section>
				</>
			)}
		</div>
	);
}