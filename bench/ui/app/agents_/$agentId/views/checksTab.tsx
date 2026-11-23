import { JobRunButton } from "@/components/jobs/jobRunButton";
import { EmptyState } from "@/components/shell/emptyState";
import { Skeleton } from "@/components/ui/skeleton";
import { Input } from "@/components/ui/input";
import { useSession } from "@/lib/session/sessionContext";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import type { Job } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { CheckCircle2, ChevronRight, Circle, CircleMinus, Loader2, XCircle } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { jobState, lastJobFor, type CheckState } from "../../../agents/views/agentDisplay";
import { useAgentJobs } from "../../../agents/views/agentJobs";
import { type AgentOpsTabProps, Field, useAgentId, useAgentSummary } from "./agentOps.shared";

const DEFAULT_SUITE = "coding-core";

type StepState = CheckState | "skipped";

/**
 * The Checks tab of an agent as a readiness checklist: a free conformance check (agent.check), an
 * image build (agent.build; hosted builds run on a runner) and a version diff (agent.diff, local;
 * hosted opens the Diff page). Each row shows its last result; each button is a background job with a live log.
 */
export function ChecksTab(props: AgentOpsTabProps = {}) {
	const id = useAgentId(props);
	const { agent, isLoading } = useAgentSummary(id);
	const { t } = useTranslation("agentops");
	const { t: tu } = useTranslation("agentsUx");
	const session = useSession();
	const jobs = useAgentJobs();
	const hosted = session.authMode === "hosted";
	const [suite, setSuite] = useState(DEFAULT_SUITE);
	const [task, setTask] = useState("");
	const [noBuild, setNoBuild] = useState(false);
	const [buildSuite, setBuildSuite] = useState("");

	if (isLoading) {
		return (
			<div className="flex flex-col gap-3" data-testid="agent-checks-loading">
				{[0, 1, 2].map((i) => (
					<Skeleton key={i} className="h-16 rounded-xl" />
				))}
			</div>
		);
	}
	if (!agent) {
		return (
			<EmptyState
				compact
				message={t("checks.noAgent", { id })}
				action={{ label: t("checks.newAgent"), to: "/agents/new" }}
				testId="checks-no-agent"
			/>
		);
	}
	const checkParams = {
		agent: agent.path,
		suite: suite.trim(),
		...(task.trim() ? { task: task.trim() } : {}),
		...(noBuild ? { no_build: true } : {}),
	};
	const buildParams = { agent: agent.path, ...(buildSuite.trim() ? { suite: buildSuite.trim() } : {}) };
	const checkJob = lastJobFor(jobs, "agent.check", agent.path, agent.id);
	const buildJob = lastJobFor(jobs, "agent.build", agent.path, agent.id);
	const checkState: StepState = agent.error ? "broken" : jobState(checkJob);
	const buildState: StepState = agent.dockerfile ? jobState(buildJob) : "skipped";
	const needBuildSuite = !!agent.per_suite_build && !buildSuite.trim();
	const builtTag = buildState === "passed" && typeof buildJob?.result?.tag === "string" ? buildJob.result.tag : "";
	const diffState: StepState = jobState(lastJobFor(jobs, "agent.diff", agent.path, agent.id));

	return (
		<div className="flex flex-col gap-4" data-testid="agent-checks-tab">
			<div>
				<h2 className="text-base font-semibold tracking-tight">{tu("checks.title")}</h2>
				<p className="text-muted-foreground text-sm">{tu("checks.subtitle")}</p>
			</div>
			<ol className="bg-card divide-y rounded-xl border">
				<CheckStep
					n={1}
					title={tu("checks.step.check")}
					hint={tu("checks.hint.check")}
					detail={t("checks.check.hint")}
					state={checkState}
					testId="agent-check-section"
					problem={checkState === "failed" ? <StepProblem job={checkJob} testId="agent-check-section-error" /> : undefined}
					action={
						suite.trim() === "" ? (
							<p className="text-destructive text-xs">{t("checks.check.needSuite", { suite: DEFAULT_SUITE })}</p>
						) : (
							<div className="flex flex-wrap items-center gap-2">
								<JobRunButton kind="agent.check" params={checkParams} label={t("checks.check.run")} />
								{hosted && (
									<JobRunButton
										kind="agent.check"
										params={{ ...checkParams, real: true }}
										label={t("checks.check.tryReal")}
										variant="outline"
									/>
								)}
							</div>
						)
					}
				>
					<div className="grid gap-3 sm:grid-cols-3">
						<Field label={t("checks.check.suite")}>
							<Input value={suite} onChange={(e) => setSuite(e.target.value)} data-testid="agent-check-suite" />
						</Field>
						<Field label={t("checks.check.task")}>
							<Input value={task} onChange={(e) => setTask(e.target.value)} data-testid="agent-check-task" />
						</Field>
						{hosted && (
							<p className="text-muted-foreground text-xs sm:col-span-3" data-testid="agent-check-try-real-hint">
								{t("checks.check.tryRealHint")}
							</p>
						)}
						{!hosted && (
							<label className="flex items-center gap-2 self-end pb-2 text-xs">
								<input type="checkbox" checked={noBuild} onChange={(e) => setNoBuild(e.target.checked)} data-testid="agent-check-nobuild" />
								{t("checks.check.noBuild")}
							</label>
						)}
					</div>
				</CheckStep>

				<CheckStep
					n={2}
					title={tu("checks.step.build")}
					hint={agent.dockerfile ? tu("checks.hint.build") : tu("checks.hint.buildNone")}
					detail={
						agent.per_suite_build
							? t("checks.build.hintPerSuite")
							: agent.dockerfile
								? t("checks.build.hintDockerfile", { image: agent.image, dockerfile: agent.dockerfile })
								: t("checks.build.hintPrebuilt")
					}
					state={buildState}
					testId="agent-build-section"
					problem={
						buildState === "failed" ? (
							<StepProblem job={buildJob} testId="agent-build-section-error" />
						) : builtTag ? (
							<p className="text-xs" data-testid="agent-build-ready">
								{t("checks.build.ready", { tag: builtTag })}
							</p>
						) : undefined
					}
					action={
						agent.dockerfile ? (
							<div className="flex flex-col items-end gap-1">
								<JobRunButton
									kind="agent.build"
									params={buildParams}
									label={t("checks.build.run")}
									variant="outline"
									disabled={needBuildSuite}
								/>
								{needBuildSuite && (
									<p className="text-muted-foreground text-xs" data-testid="agent-build-pick-first">
										{t("checks.build.pickFirst")}
									</p>
								)}
							</div>
						) : null
					}
				>
					{hosted && agent.dockerfile && (
						<p className="text-muted-foreground text-xs" data-testid="agent-build-runner-note">
							{t("checks.build.runnerNote")}
						</p>
					)}
					{!agent.dockerfile ? (
						<p className="text-muted-foreground text-xs">{t("checks.build.nothing")}</p>
					) : agent.per_suite_build ? (
						<Field label={t("checks.build.suiteRequired")}>
							<BuildSuiteSelect value={buildSuite} onChange={setBuildSuite} placeholder={t("checks.build.pickPlaceholder")} />
						</Field>
					) : (
						<Field label={t("checks.build.suiteOptional")}>
							<Input value={buildSuite} onChange={(e) => setBuildSuite(e.target.value)} data-testid="agent-build-suite" />
						</Field>
					)}
				</CheckStep>

				<CheckStep
					n={3}
					title={tu("checks.step.diff")}
					hint={tu("checks.hint.diff")}
					detail={t("checks.diff.hint")}
					state={diffState}
					testId="agent-diff-section"
					action={
						<div className="flex flex-wrap items-center gap-3">
							{!hosted && <JobRunButton kind="agent.diff" params={{ agent: agent.id }} label={t("checks.diff.run")} variant="outline" />}
							<Link
								to="/agents"
								search={{ tab: "diff", agent: agent.id }}
								className="text-primary text-sm hover:underline"
								data-testid="agent-diff-link"
							>
								{t("checks.diff.open")}
							</Link>
						</div>
					}
				/>
			</ol>
		</div>
	);
}

/** The org's benchmarks from the light list, for the per-benchmark container build (N181). */
function BuildSuiteSelect({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder: string }) {
	const { data } = useGetSuitesLightQuery();
	const ids = (data?.suites ?? []).map((s) => s.id).sort();
	return (
		<select
			className="border-input bg-card h-9 rounded-md border px-2 text-sm"
			value={value}
			onChange={(e) => onChange(e.target.value)}
			data-testid="agent-build-suite-select"
		>
			<option value="">{placeholder}</option>
			{ids.map((id) => (
				<option key={id} value={id}>
					{id}
				</option>
			))}
		</select>
	);
}

/** Why a readiness step failed: the job's error (first 300 characters) and its log (N180). */
function StepProblem({ job, testId }: { job: Job | undefined; testId: string }) {
	const { t } = useTranslation("agentops");
	if (!job) return null;
	const error = (job.error ?? "").trim();
	return (
		<div className="flex flex-col gap-1 text-xs" data-testid={testId}>
			{error && <p className="text-destructive break-words">{error.length > 300 ? `${error.slice(0, 300)}...` : error}</p>}
			<Link to="/jobs/$jobId" params={{ jobId: job.id }} className="text-primary hover:underline">
				{t("checks.openLog")}
			</Link>
		</div>
	);
}

const ICON: Record<StepState, { icon: typeof Circle; cls: string }> = {
	passed: { icon: CheckCircle2, cls: "text-emerald-600 dark:text-emerald-400" },
	failed: { icon: XCircle, cls: "text-red-600 dark:text-red-400" },
	broken: { icon: XCircle, cls: "text-red-600 dark:text-red-400" },
	running: { icon: Loader2, cls: "text-sky-600 animate-spin dark:text-sky-400" },
	never: { icon: Circle, cls: "text-muted-foreground/60" },
	skipped: { icon: CircleMinus, cls: "text-muted-foreground/60" },
};

/** One row of the checklist: status icon, title and short hint, the action, and options on demand. */
function CheckStep({
	n,
	title,
	hint,
	detail,
	state,
	action,
	children,
	testId,
	problem,
}: {
	n: number;
	title: string;
	hint: string;
	detail?: string;
	state: StepState;
	action?: ReactNode;
	children?: ReactNode;
	testId: string;
	problem?: ReactNode;
}) {
	const { t } = useTranslation("agentsUx");
	const { icon: Icon, cls } = ICON[state];
	return (
		<li className="flex flex-col gap-3 p-5" data-testid={testId} data-state={state}>
			<div className="flex flex-wrap items-start gap-4">
				<Icon className={cn("mt-0.5 size-5 shrink-0", cls)} aria-hidden="true" />
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<h3 className="text-sm font-semibold">
						<span className="text-muted-foreground mr-1.5 tabular-nums">{n}.</span>
						{title}
					</h3>
					<p className="text-muted-foreground text-sm" title={detail}>
						{hint}
					</p>
					<span className={cn("text-xs font-medium", cls.replace("animate-spin", ""))} data-testid={`${testId}-state`}>
						{t(`checks.state.${state}`)}
					</span>
					{problem}
				</div>
				{action && <div className="shrink-0">{action}</div>}
			</div>
			{children && (
				<details className="group ml-9">
					<summary className="text-muted-foreground hover:text-foreground flex w-fit cursor-pointer list-none items-center gap-1 text-xs">
						<ChevronRight className="size-3.5 transition-transform group-open:rotate-90" aria-hidden="true" />
						{t("checks.options")}
					</summary>
					<div className="mt-3 flex flex-col gap-3">
						{detail && <p className="text-muted-foreground text-xs">{detail}</p>}
						{children}
					</div>
				</details>
			)}
		</li>
	);
}

export default ChecksTab;