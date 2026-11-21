import { Pill, StatCard } from "@/components/home/homeBits";
import { homeStats, monthStart, splitExperiments, type ExperimentListItem } from "@/components/home/home.utils";
import { RecentExperiments } from "@/components/home/recentExperiments";
import { RunningExperiments } from "@/components/home/runningExperiments";
import { SuitesPanel } from "@/components/home/suitesPanel";
import { LoadError } from "@/components/runs/screenStates";
import { EmptyState, suiteIdOf } from "@/components/shell";
import { welcomeSeen } from "@/app/welcome/views/welcome.utils";
import { Button } from "@/components/ui/button";
import { formatValueNumber } from "@/components/valueCell.utils";
import { getLocale } from "@/lib/i18n";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetSuitesQuery, useListExperimentsQuery } from "@/lib/store";
import { useGetHealthQuery, useListRunsQuery } from "@/lib/store/apis/benchApi";
import { launchAvailable } from "@/lib/types/bench";
import "@/lib/types/homeI18n";
import { cn } from "@/lib/utils";
import { Link, Navigate } from "@tanstack/react-router";
import { ArrowRight, Boxes, CircleCheck, FlaskConical, Play, Plus, Rocket, Sparkles, Wallet } from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** How often the list itself refreshes, so a new or finished experiment shows up. */
const LIST_POLL_MS = 30_000;
/** Newest runs read for the KPI row (the server's page cap). */
const RUNS_PAGE = 500;

function greetingKey(now: Date): "morning" | "afternoon" | "evening" {
	const hour = now.getHours();
	return hour < 12 ? "morning" : hour < 18 ? "afternoon" : "evening";
}

/**
 * Home: a greeting with the one thing to do, four KPIs, the experiments running now with live
 * progress, the newest finished ones and the suites. A fresh org gets one onboarding card that
 * leads to the setup wizard.
 */
export default function HomeView() {
	const { t } = useTranslation("home");
	const locale = getLocale();
	const list = useListExperimentsQuery(undefined, { pollingInterval: LIST_POLL_MS, skipPollingIfUnfocused: true });
	const suites = useGetSuitesQuery();
	const runs = useListRunsQuery({ limit: RUNS_PAGE }, { pollingInterval: LIST_POLL_MS, skipPollingIfUnfocused: true });
	const launch = launchAvailable(useGetHealthQuery().data);
	const experiments = (list.data?.experiments ?? []) as ExperimentListItem[];
	const { running, recent } = splitExperiments(experiments);
	const empty = list.isSuccess && experiments.length === 0;
	const session = useSession();
	const mayRun = canUse(session, "member");
	const now = new Date();
	const stats = homeStats(experiments, runs.data?.runs ?? [], now);

	const tracks = useMemo(() => new Map((suites.data?.suites ?? []).map((suite) => [suite.id, suite.track as string | undefined])), [suites.data]);
	const trackOf = (suite: string | undefined) => (suite ? tracks.get(suiteIdOf(suite)) : undefined);

	// First sign-in to an org with no experiment: open the setup wizard once per browser.
	if (empty && session.signedIn && mayRun && !welcomeSeen()) {
		return <Navigate to="/welcome" replace />;
	}

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
					<p className="text-muted-foreground text-sm">{t("subtitle")}</p>
				</div>
				{mayRun && (
					<div className="flex shrink-0 flex-wrap gap-2">
						{launch && (
							<Button asChild variant="outline" dataTestId="home-cta-new">
								<Link to="/experiments/new">
									<Plus aria-hidden="true" />
									{t("cta.newExperiment")}
								</Link>
							</Button>
						)}
						<Button asChild dataTestId="home-cta-run">
							<Link to="/welcome">
								<Play aria-hidden="true" />
								{t("cta.run")}
							</Link>
						</Button>
					</div>
				)}
			</header>

			{empty && <OnboardingCard mayRun={mayRun} />}

			<div className="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-4" data-testid="home-kpis">
				<StatCard
					icon={FlaskConical}
					label={t("kpi.experiments")}
					value={stats.experiments}
					hint={t("kpi.experimentsHint", { count: stats.running })}
					loading={list.isLoading}
					testId="home-kpi-experiments"
				/>
				<StatCard
					icon={Rocket}
					label={t("kpi.runsWeek")}
					value={runs.error ? "-" : stats.runsWeek}
					hint={t("kpi.runsWeekHint")}
					loading={runs.isLoading}
					testId="home-kpi-runs"
				/>
				<StatCard
					icon={CircleCheck}
					label={t("kpi.passRate")}
					value={runs.error || stats.passRate === null ? "-" : `${Math.round(stats.passRate * 100)}%`}
					hint={stats.graded > 0 ? t("kpi.passRateHint", { pass: stats.passed, total: stats.graded }) : t("kpi.passRateNone")}
					loading={runs.isLoading}
					testId="home-kpi-pass"
				/>
				<StatCard
					icon={Wallet}
					label={t("kpi.spend")}
					value={usd(stats.spendMonth)}
					hint={t("kpi.spendHint", { date: monthStart(now).toLocaleDateString(locale, { month: "short", day: "numeric" }) })}
					loading={list.isLoading}
					testId="home-kpi-spend"
				/>
			</div>

			{!empty && (
				<HomeSection
					title={t("running.title")}
					badge={running.length > 0 ? <Pill tone="info">{running.length}</Pill> : undefined}
					testId="home-section-running"
				>
					{list.isLoading ? (
						<div className="grid gap-4 md:grid-cols-2" data-testid="home-running-loading" aria-hidden="true">
							<div className="bg-muted h-44 animate-pulse rounded-xl" />
							<div className="bg-muted hidden h-44 animate-pulse rounded-xl md:block" />
						</div>
					) : list.error ? null : (
						<RunningExperiments experiments={running} onFinished={() => void list.refetch()} trackOf={trackOf} />
					)}
				</HomeSection>
			)}

			<div className="grid min-w-0 gap-8 lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)] lg:gap-6">
				{!empty && (
					<HomeSection title={t("recent.title")} testId="home-section-recent" link={{ to: "/experiments", label: t("recent.viewAll") }}>
						<div className="bg-card overflow-hidden rounded-xl border shadow-(--shadow-card)">
							{list.isLoading ? (
								<ListSkeleton rows={5} testId="home-recent-loading" />
							) : list.error ? (
								<LoadError
									title={t("recent.title")}
									error={list.error}
									onRetry={list.refetch}
									testId="home-recent-error"
									className="m-3"
								/>
							) : recent.length === 0 ? (
								<EmptyState
									icon={FlaskConical}
									message={t("recent.empty")}
									action={mayRun ? { label: t("cta.run"), to: "/welcome" } : undefined}
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
					{suites.isLoading ? (
						<div className="grid gap-3 sm:grid-cols-2" data-testid="home-suites-loading" aria-hidden="true">
							{[0, 1, 2, 3].map((i) => (
								<div key={i} className="bg-muted h-16 animate-pulse rounded-xl" />
							))}
						</div>
					) : suites.error ? (
						<LoadError title={t("suites.title")} error={suites.error} onRetry={suites.refetch} testId="home-suites-error" />
					) : (suites.data?.suites.length ?? 0) === 0 ? (
						<EmptyState
							icon={Boxes}
							message={t("suites.empty")}
							action={{ label: t("suites.browse"), to: "/catalog" }}
							compact
							testId="home-suites-empty"
						/>
					) : (
						<SuitesPanel suites={suites.data?.suites ?? []} />
					)}
				</HomeSection>
			</div>
		</section>
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
						className="text-muted-foreground hover:text-foreground focus-visible:ring-ring/60 ml-auto inline-flex items-center gap-1 rounded-sm text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:outline-none"
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

function ListSkeleton({ rows, testId }: { rows: number; testId: string }) {
	return (
		<div className="flex flex-col divide-y" data-testid={testId} aria-hidden="true">
			{Array.from({ length: rows }, (_, i) => (
				<div key={i} className="flex items-center gap-3 px-5 py-3">
					<span className="bg-muted size-8 animate-pulse rounded-lg" />
					<span className="flex flex-1 flex-col gap-1.5">
						<span className="bg-muted h-3.5 w-2/5 animate-pulse rounded" />
						<span className="bg-muted h-3 w-1/4 animate-pulse rounded" />
					</span>
					<span className="bg-muted h-5 w-20 animate-pulse rounded-full" />
				</div>
			))}
		</div>
	);
}

/** The one onboarding card on a fresh org: a big way into the /welcome wizard. */
function OnboardingCard({ mayRun }: { mayRun: boolean }) {
	const { t } = useTranslation("home");
	return (
		<div
			className="border-primary/25 from-primary/10 via-card to-card flex flex-col gap-5 rounded-2xl border bg-gradient-to-br p-6 shadow-(--shadow-card) sm:flex-row sm:items-center md:p-8"
			data-testid="home-welcome-card"
		>
			<span className="bg-primary text-primary-foreground flex size-12 shrink-0 items-center justify-center rounded-2xl shadow-sm">
				<Sparkles className="size-6" aria-hidden="true" />
			</span>
			<div className="flex min-w-0 flex-1 flex-col gap-1.5">
				<h2 className="text-lg font-semibold tracking-tight">{t("onboarding.title")}</h2>
				<p className="text-muted-foreground max-w-xl text-sm">{mayRun ? t("onboarding.body") : t("onboarding.viewer")}</p>
			</div>
			{mayRun && (
				<Button asChild size="lg" className="shrink-0">
					<Link to="/welcome" data-testid="home-welcome-open">
						{t("onboarding.open")}
						<ArrowRight className="size-4" aria-hidden="true" />
					</Link>
				</Button>
			)}
		</div>
	);
}

