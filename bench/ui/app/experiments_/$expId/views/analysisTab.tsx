import { BlameReplay } from "@/components/experiments/blameReplay";
import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Button } from "@/components/ui/button";
import { ActionCard } from "@/components/experiments/compareActionCard";
import { AUDIT_MAX_BYTES, useAuditTableMutation } from "@/lib/store/apis/analysisApi";
import { getBenchErrorMessage, useGetClustersQuery, useGetExperimentQuery, useGetVerdictQuery } from "@/lib/store/apis/benchApi";
import { NotScannedList } from "@/components/integrity/awarenessPanel";
import { useGetAwarenessQuery } from "@/lib/store/apis/integrityApi";
import { useGetJobQuery } from "@/lib/store/apis/platformApi";
import type { AnalysisJobKind, AnalysisView, BlameRunResult, CheckRunResult, ClustersRebuildResult, CodeScanRunResult } from "@/lib/types/analysis";
import type { ExperimentStatus, Job, JobStatus } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { ArrowUpRight, Coins, Database, Eye, FileSpreadsheet, Gavel, Network, ScanSearch, ShieldCheck, type LucideIcon } from "lucide-react";
import type { TFunction } from "i18next";
import { cn } from "@/lib/utils";
import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { useTranslation } from "react-i18next";

/** The search value of the Analysis tab (?tab=analysis). */
export const ANALYSIS_TAB_ID = "analysis";

/** Poll interval of a job the tab started, until it ends. */
export const ANALYSIS_JOB_POLL_MS = 2000;

const TERMINAL: ReadonlySet<JobStatus> = new Set(["succeeded", "failed", "cancelled"]);

/** One analysis job: its kind, the i18n key of its texts and the view a finished one refreshes. */
export interface AnalysisJobSpec {
	kind: AnalysisJobKind;
	key: "check" | "codescan" | "awareness" | "blame" | "clusters" | "index";
	/** Takes the experiment id as exp_id; the rest act on the whole org. */
	perExperiment: boolean;
	refreshes: AnalysisView[];
}

export const ANALYSIS_JOBS: readonly AnalysisJobSpec[] = [
	{
		kind: "check.run",
		key: "check",
		perExperiment: true,
		refreshes: ["verdict"],
	},
	{ kind: "codescan.run", key: "codescan", perExperiment: true, refreshes: [] },
	{
		kind: "awareness.run",
		key: "awareness",
		perExperiment: true,
		refreshes: ["awareness"],
	},
	{
		kind: "blame.run",
		key: "blame",
		perExperiment: true,
		refreshes: ["clusters"],
	},
	{
		kind: "clusters.rebuild",
		key: "clusters",
		perExperiment: false,
		refreshes: ["clusters"],
	},
	{
		kind: "index.rebuild",
		key: "index",
		perExperiment: false,
		refreshes: ["clusters", "verdict"],
	},
];

/** The icon of each analysis job card. */
const JOB_ICONS: Record<AnalysisJobSpec["key"], LucideIcon> = {
	check: Gavel,
	codescan: ShieldCheck,
	awareness: Eye,
	blame: ScanSearch,
	clusters: Network,
	index: Database,
};

/** The params a job of spec starts with. */
export function analysisJobParams(spec: AnalysisJobSpec, expId: string): Record<string, string> {
	return spec.perExperiment ? { exp_id: expId } : {};
}

/** A one-line summary of a finished job's result, or "" when there is nothing to say. */
export function summarizeAnalysisResult(t: TFunction<"analysis">, spec: AnalysisJobSpec, job: Pick<Job, "status" | "result" | "error">): string {
	if (job.status === "failed") return job.error ?? "";
	if (job.status !== "succeeded") return "";
	const r = (job.result ?? {}) as unknown;
	switch (spec.key) {
		case "check":
			return checkSummary(t, r as CheckRunResult);
		case "codescan": {
			const c = r as CodeScanRunResult;
			return t("result.codescan", {
				findings: c.findings ?? 0,
				scanned: c.scanned ?? 0,
				runs: c.runs ?? 0,
				vetoed: c.vetoed?.length ?? 0,
			});
		}
		case "blame": {
			// Runs that were not scored (infrastructure or grader problem) are counted apart from failed runs (N62).
			const b = r as BlameRunResult;
			const counts = { runs: b.runs ?? 0, failed: b.failed ?? 0, blamed: b.blamed ?? 0, notScored: b.not_scored ?? 0 };
			return counts.notScored > 0 ? t("result.blameNotScored", counts) : t("result.blame", counts);
		}
		case "clusters": {
			const c = r as ClustersRebuildResult;
			return t("result.clusters", {
				clusters: c.clusters ?? 0,
				failures: c.failures ?? 0,
				runs: c.runs ?? 0,
			});
		}
		case "index":
			return t("result.index");
		case "awareness":
			return t("result.awareness");
	}
}

const VERDICT_WORDS = ["score", "improved", "regressed", "inconclusive", "invalid"] as const;
const ACTION_WORDS = ["ship", "hold", "fix_harness"] as const;
const POLICY_WORDS = ["allow", "warn", "block"] as const;

/** One known value in words, or the value as written (never "?"). */
function wordsFor<T extends string>(known: readonly T[], value: string | undefined, say: (v: T) => string): string {
	if (!value) return "";
	return (known as readonly string[]).includes(value) ? say(value as T) : value;
}

/**
 * A verdict in plain words (N61): what the result says, what to do, and what the release rule set
 * decided, named by its file as Releases > Rules lists it. A score run has one setup and no release
 * decision, so only its sentence is said.
 */
export function checkSummary(t: TFunction<"analysis">, c: Pick<CheckRunResult, "decision" | "action" | "policy" | "policy_file">): string {
	const parts = [wordsFor(VERDICT_WORDS, c.decision, (d) => t(`result.verdict.${d}`))];
	if (c.decision !== "score") {
		// no_decision adds nothing: the verdict sentence already says there is no clear answer.
		if (c.action !== "no_decision") parts.push(wordsFor(ACTION_WORDS, c.action, (a) => t(`result.action.${a}`)));
		if (c.policy) {
			const decision = wordsFor(POLICY_WORDS, c.policy, (p) => t(`result.policyDecision.${p}`));
			// Results saved before the rule set file was recorded name no file.
			parts.push(c.policy_file ? t("result.checkPolicy", { file: c.policy_file, decision }) : t("result.checkPolicyUnnamed", { decision }));
		}
	}
	return parts.filter(Boolean).join(" ");
}

/** The tab strip label, translated from the analysis namespace. */
export function AnalysisTabLabel() {
	const { t } = useTranslation("analysis");
	return <>{t("tab")}</>;
}

/**
 * The Analysis tab (web parity lane L15): the analysis commands as background jobs, each refreshing
 * the view it changes when it ends, the cost views one click away, and an audit of a published table.
 */
export function AnalysisTab({ expId }: { expId: string }) {
	const { t } = useTranslation("analysis");
	const verdict = useGetVerdictQuery(expId);
	const awareness = useGetAwarenessQuery(expId);
	const clusters = useGetClustersQuery({ exp: expId });
	// multi_agent comes from the experiment status (fix wave 2, lane G04): blame splits a team run's failure between its agents.
	const experiment = useGetExperimentQuery(expId);
	const singleAgent = (experiment.data as (ExperimentStatus & { multi_agent?: boolean }) | undefined)?.multi_agent !== true;
	const notScanned = awareness.data?.not_scanned ?? [];
	const refresh = (views: AnalysisView[]) => {
		if (views.includes("verdict")) void verdict.refetch();
		if (views.includes("awareness")) void awareness.refetch();
		if (views.includes("clusters")) void clusters.refetch();
	};

	const { t: tu } = useTranslation("compareUx");
	return (
		<div className="flex flex-col gap-8" data-testid="analysis-screen">
			<p className="text-muted-foreground text-sm">{tu("analysis.intro")}</p>

			<section className="flex flex-col gap-3" aria-label={tu("analysis.views")}>
				<h2 className="text-base font-semibold tracking-tight">{tu("analysis.views")}</h2>
				<div className="grid gap-3 md:grid-cols-2">
					<ViewCard icon={Gavel} title={t("views.verdict")} testId="analysis-verdict">
						{verdict.data ? (
							<span className="text-foreground text-base font-semibold" data-testid="analysis-verdict-decision">
								{checkSummary(t, { decision: verdict.data.verdict.decision, action: verdict.data.verdict.action })}
							</span>
						) : verdict.isFetching ? (
							<span className="bg-muted block h-5 w-40 animate-pulse rounded" />
						) : (
							<span className="text-muted-foreground">{t("views.verdictNone")}</span>
						)}
					</ViewCard>

					<ViewCard icon={Eye} title={t("views.awareness")} testId="analysis-awareness">
						{awareness.data ? (
							<div className="flex flex-col gap-2">
								{Object.entries(awareness.data.per_arm)
									.sort(([a], [b]) => a.localeCompare(b))
									.map(([arm, a]) => (
										<div
											key={arm}
											className="flex flex-col gap-1"
											title={t("views.awarenessArm", {
												arm,
												withSignals: a.runs_with_signals,
												scanned: a.runs_scanned,
												signals: a.signals,
											})}
										>
											<span className="flex justify-between text-xs">
												<span className="font-medium">{arm}</span>
												<span className="text-muted-foreground tabular-nums">
													{a.runs_with_signals} / {a.runs_scanned}
												</span>
											</span>
											<span className="bg-muted h-1.5 overflow-hidden rounded-full">
												<span
													className="bg-chart-warning block h-full rounded-full"
													style={{
														width: `${a.runs_scanned > 0 ? (100 * a.runs_with_signals) / a.runs_scanned : 0}%`,
													}}
												/>
											</span>
											<span className="sr-only">
												{t("views.awarenessArm", {
													arm,
													withSignals: a.runs_with_signals,
													scanned: a.runs_scanned,
													signals: a.signals,
												})}
											</span>
										</div>
									))}
								{notScanned.length > 0 && (
									<span className="text-muted-foreground text-xs" data-testid="analysis-awareness-not-scanned">
										{t("views.awarenessNotScanned", { count: notScanned.length })}
									</span>
								)}
								<NotScannedList runs={notScanned} />
								<span className="text-muted-foreground line-clamp-2 text-xs">{awareness.data.label}</span>
							</div>
						) : (
							<span className="text-muted-foreground">
								{awareness.error
									? t("views.loadError", {
											error: getBenchErrorMessage(awareness.error),
										})
									: t("views.awarenessNone")}
							</span>
						)}
					</ViewCard>

					<ViewCard
						icon={Network}
						title={t("views.clusters")}
						testId="analysis-clusters"
						className="md:col-span-2"
						action={
							<Link
								to="/inbox"
								search={{ tab: "clusters", exp: expId } as never}
								className="text-primary inline-flex items-center gap-1 text-xs font-medium hover:underline"
								data-testid="analysis-clusters-link"
							>
								{t("views.clustersAll")}
								<ArrowUpRight className="size-3.5" aria-hidden="true" />
							</Link>
						}
					>
						{clusters.error ? (
							<span className="text-muted-foreground">
								{t("views.loadError", {
									error: getBenchErrorMessage(clusters.error),
								})}
							</span>
						) : clusters.data && clusters.data.clusters.length > 0 ? (
							<ClusterBars clusters={clusters.data.clusters.slice(0, 8)} />
						) : (
							<span className="text-muted-foreground">{t("views.clustersNone")}</span>
						)}
					</ViewCard>
				</div>
			</section>

			<section className="flex flex-col gap-3" aria-label={t("jobs.title")}>
				<h2 className="text-base font-semibold tracking-tight">{t("jobs.title")}</h2>
				<div className="grid items-start gap-3 lg:grid-cols-2">
					{ANALYSIS_JOBS.map((spec) => (
						<AnalysisJobCard
							key={spec.kind}
							spec={spec}
							expId={expId}
							unavailable={spec.key === "blame" && singleAgent ? t("jobs.blame.single") : undefined}
							onFinished={() => refresh(spec.refreshes)}
						/>
					))}
				</div>
			</section>

			<section className="flex flex-col gap-3" aria-label={tu("analysis.tools")}>
				<h2 className="text-base font-semibold tracking-tight">{tu("analysis.tools")}</h2>
				<div className="bg-card flex flex-col gap-3 rounded-xl border p-4" data-testid="analysis-cost-links">
					<span className="flex items-center gap-2 text-sm font-semibold">
						<Coins className="text-primary size-4" aria-hidden="true" />
						{t("links.title")}
					</span>
					<div className="flex flex-wrap gap-2">
						<Link
							to="/experiments/$expId"
							params={{ expId }}
							search={{ tab: "cost" } as never}
							className={CHIP}
							data-testid="analysis-link-cost-explain"
						>
							{t("links.costExplain")}
						</Link>
						<Link to="/experiments/$expId" params={{ expId }} search={{ tab: "cost" } as never} className={CHIP} data-testid="analysis-link-pareto">
							{t("links.pareto")}
						</Link>
						<Link to="/leaderboard" className={CHIP} data-testid="analysis-link-leaderboard-pareto">
							{t("links.leaderboardPareto")}
						</Link>
					</div>
				</div>
				<AuditUpload />
			</section>
		</div>
	);
}

const CHIP = "hover:bg-muted inline-flex items-center rounded-full border px-3 py-1 text-xs font-medium transition-colors";

/** A result view: icon, title, optional link, body. */
function ViewCard({
	icon: Icon,
	title,
	action,
	children,
	testId,
	className,
}: {
	icon: LucideIcon;
	title: string;
	action?: React.ReactNode;
	children: React.ReactNode;
	testId: string;
	className?: string;
}) {
	return (
		<div className={`bg-card flex flex-col gap-3 rounded-xl border p-5 ${className ?? ""}`} data-testid={testId}>
			<div className="flex items-center justify-between gap-2">
				<span className="text-muted-foreground flex items-center gap-2 text-xs font-medium">
					<Icon className="size-4" aria-hidden="true" />
					{title}
				</span>
				{action}
			</div>
			<div className="text-sm">{children}</div>
		</div>
	);
}

/** Failure clusters as horizontal bars, largest count as the full width. */
function ClusterBars({ clusters }: { clusters: readonly { signature: string; count: number; title: string }[] }) {
	const max = Math.max(1, ...clusters.map((c) => c.count));
	return (
		<ul className="flex flex-col gap-2.5">
			{clusters.map((c) => (
				<li key={c.signature} className="flex flex-col gap-1">
					<span className="flex items-baseline justify-between gap-3">
						<span className="truncate" title={`${c.signature}: ${c.title}`}>
							{c.title}
						</span>
						<span className="tabular-nums text-xs font-semibold">{c.count}</span>
					</span>
					<span className="bg-muted h-1.5 overflow-hidden rounded-full">
						<span className="bg-destructive/70 block h-full rounded-full" style={{ width: `${(100 * c.count) / max}%` }} />
					</span>
					<span className="text-muted-foreground font-mono text-[10px]">{c.signature}</span>
				</li>
			))}
		</ul>
	);
}

/** One job button with its status and result summary; tells the tab once the job ends. */
function AnalysisJobCard({
	spec,
	expId,
	unavailable,
	onFinished,
}: {
	spec: AnalysisJobSpec;
	expId: string;
	/** When set, the job does not apply to this experiment: the button is disabled and this says why. */
	unavailable?: string;
	onFinished(): void;
}) {
	const { t } = useTranslation("analysis");
	const [jobId, setJobId] = useState<string | null>(null);
	const job = useGetJobQuery(jobId ?? "", {
		skip: !jobId,
		pollingInterval: ANALYSIS_JOB_POLL_MS,
		skipPollingIfUnfocused: true,
	});
	const status = job.data && job.data.id === jobId ? job.data.status : undefined;
	const ended = status ? TERMINAL.has(status) : false;
	const handled = useRef<string | null>(null);
	const finished = useRef(onFinished);
	finished.current = onFinished;
	useEffect(() => {
		if (jobId && ended && handled.current !== jobId) {
			handled.current = jobId;
			finished.current();
		}
	}, [jobId, ended]);
	const summary = job.data && ended ? summarizeAnalysisResult(t, spec, job.data) : "";

	const statusPill = status && (
		<span
			data-testid={`analysis-job-${spec.key}-status`}
			className={cn(
				"mt-1 inline-flex w-fit items-center gap-1.5 rounded-full px-2 py-0.5 text-xs",
				status === "failed"
					? "bg-destructive/10 text-destructive"
					: status === "succeeded"
						? "bg-chart-success/15 text-chart-success-ink"
						: "bg-primary/10 text-primary",
			)}
			title={summary || undefined}
		>
			{(status === "running" || status === "queued") && <span className="bg-primary size-1.5 animate-pulse rounded-full" />}
			<span className="line-clamp-1">
				{t(`jobs.${status === "running" || status === "queued" ? status : (status as "succeeded" | "failed" | "cancelled")}`)}
				{summary ? `: ${summary}` : ""}
			</span>
		</span>
	);
	const button = (
		<JobRunButton
			kind={spec.kind}
			params={analysisJobParams(spec, expId)}
			label={t(`jobs.${spec.key}.button`)}
			variant="outline"
			disabled={Boolean(unavailable)}
			onDone={(j) => setJobId(j.id)}
		/>
	);

	return (
		<ActionCard
			icon={JOB_ICONS[spec.key]}
			title={t(`jobs.${spec.key}.title`)}
			description={t(`jobs.${spec.key}.description`)}
			status={
				unavailable ? (
					<span className="text-muted-foreground mt-1 text-xs" data-testid={`analysis-job-${spec.key}-unavailable`}>
						{unavailable}
					</span>
				) : (
					statusPill
				)
			}
			action={button}
			testId={`analysis-job-${spec.key}`}
		>
			{spec.key === "blame" ? <BlameReplay expId={expId} /> : undefined}
		</ActionCard>
	);
}

/** POST /audit with a per-task CSV, from a file or pasted. */
function AuditUpload() {
	const { t } = useTranslation("analysis");
	const [csv, setCsv] = useState("");
	const [problem, setProblem] = useState("");
	const [audit, result] = useAuditTableMutation();
	const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
		const file = e.target.files?.[0];
		if (!file) return;
		if (file.size > AUDIT_MAX_BYTES) {
			setProblem(t("audit.tooLarge"));
			return;
		}
		setProblem("");
		setCsv(await file.text());
	};
	const submit = () => {
		if (!csv.trim()) {
			setProblem(t("audit.empty"));
			return;
		}
		if (new Blob([csv]).size > AUDIT_MAX_BYTES) {
			setProblem(t("audit.tooLarge"));
			return;
		}
		setProblem("");
		void audit({ csv });
	};
	const v = result.data;
	return (
		<ActionCard icon={FileSpreadsheet} title={t("audit.title")} description={t("audit.description")} testId="analysis-audit" defaultOpen={Boolean(v)}>
			<div className="flex flex-col gap-3 text-sm">
				<label className="flex flex-col gap-1">
					<span>{t("audit.file")}</span>
					<input type="file" accept=".csv,text/csv" onChange={(e) => void onFile(e)} data-testid="analysis-audit-file" />
				</label>
				<label className="flex flex-col gap-1">
					<span>{t("audit.paste")}</span>
					<textarea
						className="border-input bg-background min-h-24 rounded-md border p-2 font-mono text-xs"
						placeholder={t("audit.placeholder")}
						value={csv}
						onChange={(e) => setCsv(e.target.value)}
						data-testid="analysis-audit-csv"
					/>
				</label>
				<div>
					<Button onClick={submit} disabled={result.isLoading} data-testid="analysis-audit-submit">
						{t("audit.submit")}
					</Button>
				</div>
				{problem && (
					<span className="text-destructive" data-testid="analysis-audit-problem">
						{problem}
					</span>
				)}
				{result.error && (
					<span className="text-destructive" data-testid="analysis-audit-error">
						{t("audit.error", { error: getBenchErrorMessage(result.error) })}
					</span>
				)}
				{v && (
					<div className="flex flex-col gap-1" data-testid="analysis-audit-result">
						<span className="font-medium">{t("audit.result", { decision: v.decision, action: v.action })}</span>
						{v.effect && (
							<span>
								{t("audit.effect", {
									effect: (100 * v.effect.d_bar).toFixed(1),
									tasks: v.effect.n_tasks,
								})}
							</span>
						)}
						<span>{t("audit.mde", { mde: v.mde.toFixed(1) })}</span>
					</div>
				)}
			</div>
		</ActionCard>
	);
}

export default AnalysisTab;
