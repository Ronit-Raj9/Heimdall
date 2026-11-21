import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { AUDIT_MAX_BYTES, useAuditTableMutation } from "@/lib/store/apis/analysisApi";
import { getBenchErrorMessage, useGetClustersQuery, useGetVerdictQuery } from "@/lib/store/apis/benchApi";
import { useGetAwarenessQuery } from "@/lib/store/apis/integrityApi";
import { useGetJobQuery } from "@/lib/store/apis/platformApi";
import type { AnalysisJobKind, AnalysisView, BlameRunResult, CheckRunResult, ClustersRebuildResult, CodeScanRunResult } from "@/lib/types/analysis";
import type { Job, JobStatus } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
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
	{ kind: "check.run", key: "check", perExperiment: true, refreshes: ["verdict"] },
	{ kind: "codescan.run", key: "codescan", perExperiment: true, refreshes: [] },
	{ kind: "awareness.run", key: "awareness", perExperiment: true, refreshes: ["awareness"] },
	{ kind: "blame.run", key: "blame", perExperiment: true, refreshes: ["clusters"] },
	{ kind: "clusters.rebuild", key: "clusters", perExperiment: false, refreshes: ["clusters"] },
	{ kind: "index.rebuild", key: "index", perExperiment: false, refreshes: ["clusters", "verdict"] },
];

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
		case "check": {
			const c = r as CheckRunResult;
			const base = t("result.check", { decision: c.decision ?? "?", action: c.action ?? "?" });
			return c.policy ? `${base}; ${t("result.checkPolicy", { policy: c.policy })}` : base;
		}
		case "codescan": {
			const c = r as CodeScanRunResult;
			return t("result.codescan", { findings: c.findings ?? 0, scanned: c.scanned ?? 0, runs: c.runs ?? 0, vetoed: c.vetoed?.length ?? 0 });
		}
		case "blame": {
			const b = r as BlameRunResult;
			return t("result.blame", { runs: b.runs ?? 0, failed: b.failed ?? 0, blamed: b.blamed ?? 0 });
		}
		case "clusters": {
			const c = r as ClustersRebuildResult;
			return t("result.clusters", { clusters: c.clusters ?? 0, failures: c.failures ?? 0, runs: c.runs ?? 0 });
		}
		case "index":
			return t("result.index");
		case "awareness":
			return t("result.awareness");
	}
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
	const refresh = (views: AnalysisView[]) => {
		if (views.includes("verdict")) void verdict.refetch();
		if (views.includes("awareness")) void awareness.refetch();
		if (views.includes("clusters")) void clusters.refetch();
	};

	return (
		<div className="flex flex-col gap-6" data-testid="analysis-screen">
			<p className="text-muted-foreground text-sm">{t("intro")}</p>

			<section className="flex flex-col gap-3" aria-label={t("jobs.title")}>
				<h2 className="text-base font-semibold">{t("jobs.title")}</h2>
				<div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
					{ANALYSIS_JOBS.map((spec) => (
						<AnalysisJobCard key={spec.kind} spec={spec} expId={expId} onFinished={() => refresh(spec.refreshes)} />
					))}
				</div>
			</section>

			<div className="grid gap-3 md:grid-cols-2">
				<Card className="shadow-none" data-testid="analysis-verdict">
					<CardHeader>
						<CardTitle>{t("views.verdict")}</CardTitle>
					</CardHeader>
					<CardContent className="text-sm">
						{verdict.data ? (
							<span data-testid="analysis-verdict-decision">
								{t("result.check", { decision: verdict.data.verdict.decision, action: verdict.data.verdict.action })}
							</span>
						) : (
							<span className="text-muted-foreground">{verdict.isFetching ? "..." : t("views.verdictNone")}</span>
						)}
					</CardContent>
				</Card>

				<Card className="shadow-none" data-testid="analysis-awareness">
					<CardHeader>
						<CardTitle>{t("views.awareness")}</CardTitle>
					</CardHeader>
					<CardContent className="flex flex-col gap-1 text-sm">
						{awareness.data ? (
							<>
								{Object.entries(awareness.data.per_arm)
									.sort(([a], [b]) => a.localeCompare(b))
									.map(([arm, a]) => (
										<span key={arm}>
											{t("views.awarenessArm", { arm, withSignals: a.runs_with_signals, scanned: a.runs_scanned, signals: a.signals })}
										</span>
									))}
								<span className="text-muted-foreground text-xs">{awareness.data.label}</span>
							</>
						) : (
							<span className="text-muted-foreground">
								{awareness.error ? t("views.loadError", { error: getBenchErrorMessage(awareness.error) }) : t("views.awarenessNone")}
							</span>
						)}
					</CardContent>
				</Card>

				<Card className="shadow-none md:col-span-2" data-testid="analysis-clusters">
					<CardHeader>
						<CardTitle>{t("views.clusters")}</CardTitle>
						<CardDescription>
							<Link to="/inbox" search={{ tab: "clusters", exp: expId } as never} className="underline" data-testid="analysis-clusters-link">
								{t("views.clustersAll")}
							</Link>
						</CardDescription>
					</CardHeader>
					<CardContent className="text-sm">
						{clusters.error ? (
							<span className="text-muted-foreground">{t("views.loadError", { error: getBenchErrorMessage(clusters.error) })}</span>
						) : clusters.data && clusters.data.clusters.length > 0 ? (
							<ul className="flex flex-col gap-1">
								{clusters.data.clusters.slice(0, 8).map((c) => (
									<li key={c.signature} className="flex gap-2">
										<span className="w-8 text-right font-mono">{c.count}</span>
										<span className="font-mono text-xs">{c.signature}</span>
										<span>{c.title}</span>
									</li>
								))}
							</ul>
						) : (
							<span className="text-muted-foreground">{t("views.clustersNone")}</span>
						)}
					</CardContent>
				</Card>
			</div>

			<Card className="shadow-none" data-testid="analysis-cost-links">
				<CardHeader>
					<CardTitle>{t("links.title")}</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-1 text-sm">
					<Link to="/experiments/$expId" params={{ expId }} search={{ tab: "cost" } as never} className="underline" data-testid="analysis-link-cost-explain">
						{t("links.costExplain")}
					</Link>
					<Link to="/experiments/$expId" params={{ expId }} search={{ tab: "cost" } as never} className="underline" data-testid="analysis-link-pareto">
						{t("links.pareto")}
					</Link>
					<Link to="/leaderboard" className="underline" data-testid="analysis-link-leaderboard-pareto">
						{t("links.leaderboardPareto")}
					</Link>
				</CardContent>
			</Card>

			<AuditUpload />
		</div>
	);
}

/** One job button with its status and result summary; tells the tab once the job ends. */
function AnalysisJobCard({ spec, expId, onFinished }: { spec: AnalysisJobSpec; expId: string; onFinished(): void }) {
	const { t } = useTranslation("analysis");
	const [jobId, setJobId] = useState<string | null>(null);
	const job = useGetJobQuery(jobId ?? "", { skip: !jobId, pollingInterval: ANALYSIS_JOB_POLL_MS, skipPollingIfUnfocused: true });
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

	return (
		<Card className="shadow-none" data-testid={`analysis-job-${spec.key}`}>
			<CardHeader>
				<CardTitle>{t(`jobs.${spec.key}.title`)}</CardTitle>
				<CardDescription>{t(`jobs.${spec.key}.description`)}</CardDescription>
			</CardHeader>
			<CardContent className="flex flex-col gap-2 text-sm">
				<div>
					<JobRunButton
						kind={spec.kind}
						params={analysisJobParams(spec, expId)}
						label={t(`jobs.${spec.key}.button`)}
						variant="outline"
						onDone={(j) => setJobId(j.id)}
					/>
				</div>
				{status && (
					<span data-testid={`analysis-job-${spec.key}-status`} className={status === "failed" ? "text-destructive" : undefined}>
						{t(`jobs.${status === "running" || status === "queued" ? status : (status as "succeeded" | "failed" | "cancelled")}`)}
						{summary ? `: ${summary}` : ""}
					</span>
				)}
			</CardContent>
		</Card>
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
		<Card className="shadow-none" data-testid="analysis-audit">
			<CardHeader>
				<CardTitle>{t("audit.title")}</CardTitle>
				<CardDescription>{t("audit.description")}</CardDescription>
			</CardHeader>
			<CardContent className="flex flex-col gap-3 text-sm">
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
						{v.effect && <span>{t("audit.effect", { effect: (100 * v.effect.d_bar).toFixed(1), tasks: v.effect.n_tasks })}</span>}
						<span>{t("audit.mde", { mde: v.mde.toFixed(1) })}</span>
					</div>
				)}
			</CardContent>
		</Card>
	);
}

export default AnalysisTab;
