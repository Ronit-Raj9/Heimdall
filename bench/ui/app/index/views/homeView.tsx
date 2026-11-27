import { Pill, StatCard } from "@/components/home/homeBits";
import {
	benchmarkReadiness,
	evaluationStats,
	hasFinishedEvaluation,
	homeStats,
	homeUsageRange,
	monthStartUTC,
	newestEvaluations,
	queryState,
	recentComparisons,
	runningNow,
	setupSteps,
	type ExperimentListItem,
	type RunningNow,
} from "@/components/home/home.utils";
import { RecentEvaluations } from "@/components/home/recentEvaluations";
import { YourAgents, agentsWithResults, ownAgents } from "@/components/home/yourAgents";
import { RecentExperiments } from "@/components/home/recentExperiments";
import { RunningExperiments } from "@/components/home/runningExperiments";
import { SuitesPanel } from "@/components/home/suitesPanel";
import { CardGridSkeleton, LoadError, TableSkeleton } from "@/components/runs/screenStates";
import { EmptyState, suiteIdOf } from "@/components/shell";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import { useGetUsageQuery } from "@/lib/store/apis/usageApi";
import { formatValueNumber } from "@/components/valueCell.utils";
import { getLocale } from "@/lib/i18n";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetAgentsQuery, useListExperimentsQuery } from "@/lib/store";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import "@/lib/types/agentFirstI18n";
import { useListRunsQuery } from "@/lib/store/apis/benchApi";
import { useListEvaluationsQuery, type Evaluation } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/homeI18n";
import "@/components/home/uf2aI18n";
import { isTestObject } from "@/lib/testObjects";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ArrowRight, Award, Bot, Boxes, Check, CircleCheck, FlaskConical, Wallet } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** How often the list itself refreshes, so a new or finished experiment shows up. */
const LIST_POLL_MS = 30_000;
/** Evaluations listed on Home. */
const EVALUATIONS_SHOWN = 5;
/** Newest runs read for the KPI row (the server's page cap). */
const RUNS_PAGE = 500;

function greetingKey(now: Date): "morning" | "afternoon" | "evening" {
	const hour = now.getHours();
	return hour < 12 ? "morning" : hour < 18 ? "afternoon" : "evening";
}

/**
 * Home: a greeting with the one thing to do (evaluate your agent), four agent KPIs, the
 * experiments running now with live progress, the newest evaluations, the newest finished
 * comparisons and the suites. Until an evaluation has finished, a set-up card lists the four steps
 * to the first report card (M2-10); there is no first-run redirect any more.
 */
export default function HomeView() {
	const { t } = useTranslation("home");
	const { t: ta } = useTranslation("agentFirst");
	const { t: tu } = useTranslation("uf2a");
	const locale = getLocale();
	// M2-14: lists poll only while something runs; a finished run is picked up by the next visit
	// or by RunningNowList's onFinished refetch.
	const [live, setLive] = useState(true);
	const poll = { pollingInterval: live ? LIST_POLL_MS : 0, skipPollingIfUnfocused: true };
	const agents = useGetAgentsQuery();
	const list = useListExperimentsQuery(undefined, poll);
	const suites = useGetSuitesLightQuery();
	const runs = useListRunsQuery({ limit: RUNS_PAGE }, poll);
	const evals = useListEvaluationsQuery(undefined, poll);
	const [usageRange] = useState(() => homeUsageRange());
	// M8-19: "Spent" is the Usage total of this calendar month (UTC), the same number Usage shows.
	const usage = useGetUsageQuery(usageRange);
	const models = useGetModelsQuery();
	const experiments = (list.data?.experiments ?? []) as ExperimentListItem[];
	const evaluations = evals.data ?? [];
	const evStats = evaluationStats(evaluations);
	// M1-18: Home's recent lists leave out test objects ("zz-test-..." names); the full lists have the chip.
	const recentEvaluations = newestEvaluations(
		evaluations.filter((ev) => !isTestObject(ev.spec?.name ?? ev.id)),
		EVALUATIONS_SHOWN,
	);
	// Running now: active evaluations (what the Evaluations tile counts) plus running comparisons of no
	// evaluation; checks and practice runs stay out of both lists.
	const running = runningNow(evaluations, experiments);
	const anyLive = running.total > 0;
	if (list.isSuccess && evals.isSuccess && anyLive !== live) setLive(anyLive);
	const recent = recentComparisons(experiments.filter((exp) => !isTestObject(exp.name ?? exp.exp_id)));
	const empty = list.isSuccess && experiments.length === 0;
	const session = useSession();
	const mayRun = canUse(session, "member");
	const now = new Date();
	const stats = homeStats(experiments, runs.data?.runs ?? [], now);

	const tracks = useMemo(
		() => new Map((suites.data?.suites ?? []).map((suite) => [suite.id, suite.track as string | undefined])),
		[suites.data],
	);
	const trackOf = (suite: string | undefined) => (suite ? tracks.get(suiteIdOf(suite)) : undefined);

	const showSetup = evals.isSuccess && !hasFinishedEvaluation(evaluations);
	const agentList = agents.data?.agents ?? [];
	const bench = benchmarkReadiness(suites.data?.suites ?? []);
	const steps = setupSteps({
		keys: (models.data?.keys.length ?? 0) > 0,
		agent: ownAgents(agentList, Number.POSITIVE_INFINITY).length > 0,
		benchmark: bench.ready > 0,
		preparing: bench.preparing,
		evaluation: evaluations.length > 0,
		finished: hasFinishedEvaluation(evaluations),
	});
	// M2-8: each tile reads its own query state; a failed load says so, never "No scored runs yet" or $0.00.
	const runsState = queryState(runs);
	const evalsState = queryState(evals);
	const usageState = queryState(usage);
	const agentsState = queryState(agents);
	const kpiError = (onRetry: () => void) => (
		<span className="inline-flex flex-wrap items-center gap-1">
			<span className="text-danger-ink">{t("kpi.loadError")}</span>
			<button type="button" className="text-foreground relative z-10 font-medium underline underline-offset-4" onClick={onRetry}>
				{t("kpi.retry")}
			</button>
		</span>
	);

	const firstName = session.user?.name?.trim().split(/\s+/)[0];
	const greeting = t(`greeting.${greetingKey(now)}`);
	const usd = (v: number) => formatValueNumber(v, "usd", locale);

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="home-screen">
			<header className="flex min-w-0 flex-col gap-4 sm:flex-row sm:items-end sm:justify-between" data-testid="home-header">
				<div className="flex min-w-0 flex-col gap-1">
					<h1 className="text-foreground text-2xl font-semibold tracking-tight sm:text-3xl" data-testid="home-greeting">
						{firstName ? t("greeting.named", { greeting, name: firstName }) : greeting}
					</h1>
					<p className="text-muted-foreground max-w-2xl text-sm" data-testid="home-intro">
						{t("intro")}
					</p>
				</div>
			</header>

			{showSetup && <SetupCard mayRun={mayRun} steps={steps} />}

			{/* Q-302: no row of zeros before the first evaluation; the set-up card says what to do. */}
			{!(evals.isSuccess && evaluations.length === 0) && (
			<div className="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-4" data-testid="home-kpis">
				<KpiLink to="/evaluations">
					<StatCard
						icon={Bot}
						label={t("kpi.agents")}
						value={evalsState === "error" || agentsState === "error" || !agents.data ? "-" : agentsWithResults(agentList, evaluations)}
						hint={
							evalsState === "error"
								? kpiError(() => void evals.refetch())
								: agentsState === "error"
									? kpiError(() => void agents.refetch())
									: t("kpi.agentsHint")
						}
						loading={evalsState === "loading" || agentsState === "loading"}
						testId="home-kpi-agents"
					/>
				</KpiLink>
				<KpiLink to="/evaluations">
					<StatCard
						icon={Award}
						label={t("kpi.evaluations")}
						value={evalsState === "error" ? "-" : evStats.evaluations}
						hint={evalsState === "error" ? kpiError(() => void evals.refetch()) : t("kpi.evaluationsHint", { count: evStats.active })}
						loading={evalsState === "loading"}
						testId="home-kpi-evaluations"
					/>
				</KpiLink>
				<KpiLink to="/runs">
					<StatCard
						icon={CircleCheck}
						label={t("kpi.passRate")}
						value={runsState !== "ready" || stats.passRate === null ? "-" : `${Math.round(stats.passRate * 100)}%`}
						hint={
							runsState === "error"
								? kpiError(() => void runs.refetch())
								: runsState === "loading"
									? undefined
									: stats.graded > 0
									? t("kpi.passRateHint", { pass: stats.passed, total: stats.graded })
										: t("kpi.passRateNone")
						}
						loading={runsState === "loading"}
						testId="home-kpi-pass"
					/>
				</KpiLink>
				<KpiLink to="/settings" search={{ tab: "usage" }}>
					<StatCard
						icon={Wallet}
						label={t("kpi.spend")}
						value={usageState !== "ready" || !usage.data ? "-" : usd(usage.data.total_cost_usd)}
						hint={
							usageState === "error"
								? kpiError(() => void usage.refetch())
								: t("kpi.spendHint", {
										date: monthStartUTC(now).toLocaleDateString(locale, { month: "short", day: "numeric", timeZone: "UTC" }),
									})
						}
						loading={usageState === "loading"}
						testId="home-kpi-spend"
					/>
				</KpiLink>
			</div>
			)}

			<HomeSection title={ta("home.title")} testId="home-section-agents" link={{ to: "/agents", label: ta("home.viewAll") }}>
				{agentsState === "error" ? (
					<LoadError title={ta("home.title")} error={agents.error} onRetry={agents.refetch} testId="home-agents-error" />
				) : agentsState === "loading" ? (
					<CardGridSkeleton count={3} columns={3} testId="home-agents-loading" />
				) : (
					<YourAgents agents={agentList} evaluations={evaluations} mayRun={mayRun} />
				)}
			</HomeSection>

			{!empty && (
				<HomeSection
					title={t("running.title")}
					badge={running.total > 0 ? <Pill tone="info">{running.total}</Pill> : undefined}
					testId="home-section-running"
				>
					{list.isError || evals.isError ? (
						<LoadError
							title={t("running.title")}
							error={list.error ?? evals.error}
							onRetry={() => {
								void list.refetch();
								void evals.refetch();
							}}
							testId="home-running-error"
						/>
					) : queryState(list) === "loading" || evalsState === "loading" ? (
						<CardGridSkeleton count={2} columns={2} testId="home-running-loading" />
					) : (
						<RunningNowList
							running={running}
							onFinished={() => {
								void list.refetch();
								void evals.refetch();
							}}
							trackOf={trackOf}
						/>
					)}
				</HomeSection>
			)}

			{(evals.error || !empty || evaluations.length > 0) && (
				<HomeSection
					title={t("evaluations.title")}
					testId="home-section-evaluations"
					link={{ to: "/evaluations", label: t("evaluations.viewAll") }}
				>
					<div className="bg-card overflow-hidden rounded-xl border shadow-(--shadow-card)">
						{evalsState === "loading" ? (
							<TableSkeleton rows={3} testId="home-evaluations-loading" />
						) : evalsState === "error" ? (
							<LoadError
								title={t("evaluations.title")}
								error={evals.error}
								onRetry={evals.refetch}
								testId="home-evaluations-error"
								className="m-3"
							/>
						) : recentEvaluations.length === 0 ? (
							<EmptyState
								icon={Award}
								message={t("evaluations.empty")}
								action={mayRun ? { label: t("cta.run"), to: "/evaluate" } : undefined}
								compact
								className="m-3"
								testId="home-evaluations-empty"
							/>
						) : (
							<RecentEvaluations evaluations={recentEvaluations} />
						)}
					</div>
				</HomeSection>
			)}

			<div className="grid min-w-0 gap-8 lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)] lg:gap-6">
				{!empty && (
					<HomeSection title={t("recent.title")} testId="home-section-recent" link={{ to: "/experiments", label: t("recent.viewAll") }}>
						<div className="bg-card overflow-hidden rounded-xl border shadow-(--shadow-card)">
							{queryState(list) === "loading" ? (
								<TableSkeleton rows={5} testId="home-recent-loading" />
							) : list.isError ? (
								<LoadError title={t("recent.title")} error={list.error} onRetry={list.refetch} testId="home-recent-error" className="m-3" />
							) : recent.length === 0 ? (
								<EmptyState
									icon={FlaskConical}
									message={t("recent.empty")}
									action={mayRun ? { label: t("cta.run"), to: "/evaluate" } : undefined}
									compact
									className="m-3"
									testId="home-recent-empty"
								/>
							) : (
								<RecentExperiments experiments={recent} trackOf={trackOf} />
							)}
						</div>
					</HomeSection>
				)}

				<HomeSection
					title={t("suites.title")}
					testId="home-section-suites"
					link={{ to: "/suites", label: t("suites.viewAll") }}
					className={cn(empty && "lg:col-span-2")}
				>
					{suites.isError ? (
						<LoadError title={t("suites.title")} error={suites.error} onRetry={suites.refetch} testId="home-suites-error" />
					) : queryState(suites) === "loading" ? (
						<CardGridSkeleton count={4} columns={2} testId="home-suites-loading" />
					) : (suites.data?.suites.length ?? 0) === 0 ? (
						<EmptyState
							icon={Boxes}
							message={t("suites.empty")}
							action={{ label: t("suites.browse"), to: "/suites", search: { tab: "catalog" } }}
							compact
							testId="home-suites-empty"
						/>
					) : (
						<SuitesPanel suites={suites.data?.suites ?? []} />
					)}
					{mayRun && (
						<Link
							to={"/suites/add" as never}
							className="text-muted-foreground hover:text-foreground w-fit text-sm font-medium underline-offset-4 hover:underline"
							data-testid="home-add-tasks"
						>
							{tu("home.addTasks")}
						</Link>
					)}
				</HomeSection>
			</div>
		</section>
	);
}

/**
 * "Running now": the active evaluations as rows that open the evaluation, then the running comparisons
 * as live cards, and "+N more" (to the running evaluations) when the list was cut.
 */
export function RunningNowList({
	running,
	onFinished,
	trackOf,
}: {
	running: RunningNow<Evaluation, ExperimentListItem>;
	onFinished?: () => void;
	trackOf?: (suite: string | undefined) => string | undefined;
}) {
	const { t } = useTranslation("home");
	if (running.evaluations.length === 0 && running.comparisons.length === 0) return <RunningExperiments experiments={[]} />;
	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid="home-running-now">
			{running.evaluations.length > 0 && (
				<div className="bg-card overflow-hidden rounded-xl border shadow-(--shadow-card)" data-testid="home-running-evaluations">
					<RecentEvaluations evaluations={running.evaluations} />
				</div>
			)}
			{running.comparisons.length > 0 && <RunningExperiments experiments={running.comparisons} onFinished={onFinished} trackOf={trackOf} />}
			{running.more > 0 && (
				<Link
					to={"/evaluations" as never}
					search={{ status: "running" } as never}
					className="text-muted-foreground hover:text-foreground w-fit text-sm font-medium"
					data-testid="home-running-more"
				>
					{t("running.more", { count: running.more })}
				</Link>
			)}
		</div>
	);
}

/** A Home section: a heading with an optional badge and "View all" link, then its body. */
function HomeSection({
	title,
	badge,
	link,
	testId,
	className,
	children,
}: {
	title: string;
	badge?: ReactNode;
	link?: { to: string; label: string };
	testId: string;
	className?: string;
	children: ReactNode;
}) {
	const id = `${testId}-title`;
	return (
		<section className={cn("flex min-w-0 flex-col gap-3", className)} data-testid={testId} aria-labelledby={id}>
			<div className="flex items-center gap-2">
				<h2 id={id} className="text-foreground text-base font-semibold tracking-tight">
					{title}
				</h2>
				{badge}
				{link && (
					<Link
						to={link.to as never}
						className="text-muted-foreground hover:text-foreground focus-visible:ring-(--focus) ml-auto inline-flex items-center gap-1 rounded-sm text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:outline-none"
					>
						{link.label}
						<ArrowRight className="size-3.5" aria-hidden="true" />
					</Link>
				)}
			</div>
			{children}
		</section>
	);
}

/** A KPI tile as a link to the page behind its number (M2-13). */
function KpiLink({ to, search, children }: { to: string; search?: Record<string, string>; children: ReactNode }) {
	return (
		<Link
			to={to as never}
			search={search as never}
			className="focus-visible:ring-(--focus) block min-w-0 rounded-xl transition-colors focus-visible:ring-2 focus-visible:outline-none [&>div]:h-full [&>div]:hover:border-primary/40"
		>
			{children}
		</Link>
	);
}

/**
 * The set-up card (M2-10): four steps to the first report card, each ticked when done, shown on
 * Home until an evaluation has finished. It replaces the one-time redirect to /evaluate.
 */
export function SetupCard({ mayRun, steps }: { mayRun: boolean; steps: ReturnType<typeof setupSteps> }) {
	const { t } = useTranslation("home");
	const { t: tu } = useTranslation("uf2a");
	const next = steps.find((s) => !s.done);
	return (
		<section className="bg-card flex flex-col gap-4 rounded-xl border p-5 sm:p-6" data-testid="home-welcome-card" aria-labelledby="home-setup-title">
			<div className="flex min-w-0 flex-col gap-1.5">
				<h2 id="home-setup-title" className="text-lg font-semibold tracking-tight">
					{t("onboarding.title")}
				</h2>
				<p className="text-muted-foreground max-w-xl text-sm">{mayRun ? t("onboarding.body") : t("onboarding.viewer")}</p>
				{!mayRun && (
					<Link
						to={"/settings" as never}
						search={{ tab: "members" } as never}
						className="text-foreground w-fit text-sm font-medium underline underline-offset-4"
						data-testid="home-setup-members"
					>
						{tu("home.members")}
					</Link>
				)}
			</div>
			{mayRun && (
				<ol className="grid min-w-0 gap-2 sm:grid-cols-2 lg:grid-cols-5" data-testid="home-setup-steps">
					{steps.map((step, i) => (
						<li key={step.id} data-testid={`home-setup-${step.id}`} data-done={step.done} data-locked={step.blockedBy ? true : undefined}>
							{step.blockedBy ? (
								// Q-302: a step that cannot be done yet is not a link; it names the step before it.
								<div className="text-muted-foreground flex min-h-12 items-center gap-3 rounded-lg border border-dashed px-3 py-2 text-sm">
									<span className="flex size-6 shrink-0 items-center justify-center rounded-full border text-xs font-semibold" aria-hidden="true">
										{i + 1}
									</span>
									<span className="flex min-w-0 flex-col">
										<span>{t(`onboarding.steps.${step.id}`)}</span>
										<span className="text-xs">{t("onboarding.first", { step: t(`onboarding.steps.${step.blockedBy}`) })}</span>
									</span>
								</div>
							) : (
							<Link
								to={step.to as never}
								search={step.search as never}
								className={cn(
									"hover:border-primary/40 flex min-h-12 items-center gap-3 rounded-lg border px-3 py-2 text-sm transition-colors",
									step === next ? "border-primary/40 bg-primary/5 font-medium" : "",
								)}
							>
								<span
									className={cn(
										"flex size-6 shrink-0 items-center justify-center rounded-full border text-xs font-semibold",
										step.done ? "bg-primary text-primary-foreground border-primary" : "text-muted-foreground",
									)}
									aria-hidden="true"
								>
									{step.done ? <Check className="size-3.5" /> : i + 1}
								</span>
								<span className="flex min-w-0 flex-col">
									<span>
										{t(`onboarding.steps.${step.id}`)}
										{step.done && <span className="sr-only"> ({t("onboarding.done")})</span>}
									</span>
									{step.id === "benchmark" && !step.done && (step.preparing ?? 0) > 0 && (
										<span className="text-muted-foreground text-xs font-normal">{t("onboarding.preparing", { count: step.preparing })}</span>
									)}
								</span>
							</Link>
							)}
						</li>
					))}
				</ol>
			)}
		</section>
	);
}
