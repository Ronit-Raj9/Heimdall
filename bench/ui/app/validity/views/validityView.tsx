import "./w449I18n";

import { flipDate } from "@/components/validity/flipBanner";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { decisionTone, TONE_TEXT } from "@/components/verdict/verdict.utils";
import { apiErrorMessage } from "@/lib/store/apis/integrationsApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { useGetJobQuery } from "@/lib/store/apis/platformApi";
import { type ValidityEntry, type ValidityStale, useGetValidityQuery } from "@/lib/store/apis/validityApi";
import { useSession } from "@/lib/session/sessionContext";
import type { JobStatus } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
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

function ChangedTable({ entries, user }: { entries: ValidityEntry[]; user?: string }) {
	const { t } = useTranslation("w449");
	const { t: tg } = useTranslation("glossary");
	const word = (d: string) => tg(`terms.${d}.label`, { defaultValue: d });
	if (entries.length === 0) {
		return (
			<p className="text-muted-foreground px-3 py-2 text-sm" data-testid="validity-empty">
				{t("page.empty")}
			</p>
		);
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
							<div className="flex items-center gap-2">
								<Link to="/experiments/$expId" params={{ expId: e.exp_id }} className="text-primary hover:underline">
									{e.exp_id}
								</Link>
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
						<TableCell className="text-muted-foreground">{e.note ?? ""}</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function StaleList({ stale }: { stale: ValidityStale[] }) {
	const { t } = useTranslation("w449");
	if (stale.length === 0) return <p className="text-muted-foreground px-3 py-2 text-sm">{t("page.noneStale")}</p>;
	return (
		<ul className="divide-border flex flex-col divide-y text-sm" data-testid="validity-stale">
			{stale.map((s) => (
				<li key={s.exp_id} className="flex min-h-8 flex-wrap items-center gap-x-2 px-3 py-2">
					<Link to="/experiments/$expId" params={{ expId: s.exp_id }} className="text-primary hover:underline">
						{s.exp_id}
					</Link>
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

/** W4-49: "Changed results", the validity ledger of the org. */
export default function ValidityView() {
	const { t } = useTranslation("w449");
	const session = useSession();
	const validity = useGetValidityQuery();
	const data = validity.data;
	const [createJob, create] = useCreateJobMutation();
	const [jobId, setJobId] = useState<string | null>(null);
	const [scanError, setScanError] = useState<string | null>(null);
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
		if (status !== "succeeded") setScanError(job.data?.error || t("page.scanFailed"));
		setJobId(null);
		void refetch();
	}, [ended, status, job.data?.error, refetch, t]);
	const scanning = Boolean(jobId) || create.isLoading;
	const user = session.user?.id ?? "local";
	const scan = async () => {
		setScanError(null);
		const res = await createJob({ kind: "validity.scan" });
		if ("error" in res) setScanError(apiErrorMessage(res.error, t("page.scanFailed")));
		else setJobId(res.data.id);
	};
	const last = lastScanAt(data?.stale ?? []);
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="validity-screen">
			<PageHeader title={t("page.heading")} subtitle={t("page.intro")} />
			{validity.isLoading ? (
				<BlockSkeleton rows={4} testId="validity-loading" />
			) : validity.isError || !data ? (
				<LoadError title={t("page.loadError")} error={validity.error} onRetry={() => void refetch()} testId="validity-error" />
			) : (
				<>
					<section className="flex flex-col gap-2" data-testid="validity-changed">
						<h2 className="text-base font-semibold">{t("page.changedHeading")}</h2>
						<div className="bg-card min-w-0 overflow-hidden rounded-xl border">
							<ChangedTable entries={data.entries} user={user} />
						</div>
					</section>
					<section className="flex flex-col gap-2" data-testid="validity-could-change">
						<h2 className="text-base font-semibold">{t("page.couldChange")}</h2>
						<div className="bg-card min-w-0 rounded-xl border">
							<StaleList stale={data.stale} />
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
							{scanError && (
								<p role="alert" className="text-chart-error-ink text-sm" data-testid="validity-scan-error">
									{scanError}
								</p>
							)}
						</div>
					</section>
				</>
			)}
		</div>
	);
}