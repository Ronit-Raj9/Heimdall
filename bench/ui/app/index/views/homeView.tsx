import { GetStartedCard, QuickActions } from "@/components/home/quickActions";
import { RecentExperiments } from "@/components/home/recentExperiments";
import { RunningExperiments } from "@/components/home/runningExperiments";
import { SuitesPanel } from "@/components/home/suitesPanel";
import { splitExperiments, type ExperimentListItem } from "@/components/home/home.utils";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
import { useGetSuitesQuery, useListExperimentsQuery } from "@/lib/store";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ArrowRight, Boxes, FlaskConical } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** How often the list itself refreshes, so a new or finished experiment shows up. */
const LIST_POLL_MS = 30_000;

/**
 * Home: running experiments with live progress, the newest finished ones with their verdicts,
 * the suites with their admission state and the quick actions. A server with no experiment yet
 * leads with the get-started card.
 */
export default function HomeView() {
	const { t } = useTranslation("bench");
	const list = useListExperimentsQuery(undefined, { pollingInterval: LIST_POLL_MS, skipPollingIfUnfocused: true });
	const suites = useGetSuitesQuery();
	const experiments = (list.data?.experiments ?? []) as ExperimentListItem[];
	const { running, recent } = splitExperiments(experiments);
	const empty = list.isSuccess && experiments.length === 0;

	return (
		<section className="flex min-w-0 flex-col gap-8" data-testid="home-screen">
			<PageHeader title={t("home.title")} subtitle={t("home.subtitle")} testId="home-header" />

			{empty && <GetStartedCard />}

			<HomeSection title={t("home.actions.title")} testId="home-section-actions">
				{/* With the get-started card on screen, its action is not repeated. */}
				<QuickActions hide={empty ? ["getStarted"] : []} />
			</HomeSection>

			{!empty && (
				<HomeSection title={t("home.running.title")} count={list.isSuccess ? running.length : undefined} testId="home-section-running">
					{list.isLoading ? (
						<BlockSkeleton rows={1} testId="home-running-loading" />
					) : list.error ? null : (
						<RunningExperiments experiments={running} onFinished={() => void list.refetch()} />
					)}
				</HomeSection>
			)}

			<div className="grid min-w-0 gap-8 lg:grid-cols-[minmax(0,1.75fr)_minmax(0,1fr)] lg:gap-6">
				{!empty && (
					<HomeSection
						title={t("home.recent.title")}
						testId="home-section-recent"
						link={{ to: "/experiments", label: t("home.recent.viewAll") }}
						framed
					>
						{list.isLoading ? (
							<BlockSkeleton rows={4} testId="home-recent-loading" className="p-3" />
						) : list.error ? (
							<LoadError
								title={t("home.recent.loadError")}
								error={list.error}
								onRetry={list.refetch}
								testId="home-recent-error"
								className="m-3"
							/>
						) : recent.length === 0 ? (
							<EmptyState
								icon={FlaskConical}
								message={t("home.recent.empty")}
								action={{ label: t("home.getStarted.open"), to: "/quickstart" }}
								compact
								className="m-3"
								testId="home-recent-empty"
							/>
						) : (
							<RecentExperiments experiments={recent} />
						)}
					</HomeSection>
				)}

				<HomeSection
					title={t("home.suites.title")}
					count={suites.data ? suites.data.suites.length : undefined}
					testId="home-section-suites"
					link={{ to: "/suites", label: t("home.suites.viewAll") }}
					framed
				>
					{suites.isLoading ? (
						<BlockSkeleton rows={4} testId="home-suites-loading" className="p-3" />
					) : suites.error ? (
						<LoadError
							title={t("home.suites.loadError")}
							error={suites.error}
							onRetry={suites.refetch}
							testId="home-suites-error"
							className="m-3"
						/>
					) : (suites.data?.suites.length ?? 0) === 0 ? (
						<EmptyState
							icon={Boxes}
							message={t("home.suites.empty")}
							action={{ label: t("home.actions.addSuite"), to: "/imports" }}
							compact
							className="m-3"
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

/** A Home section: a heading with an optional count and "all" link, then its body (framed in a card or not). */
function HomeSection({
	title,
	count,
	link,
	framed,
	testId,
	children,
}: {
	title: string;
	count?: number;
	link?: { to: string; label: string };
	framed?: boolean;
	testId: string;
	children: ReactNode;
}) {
	const id = `${testId}-title`;
	return (
		<section className="flex min-w-0 flex-col gap-3" data-testid={testId} aria-labelledby={id}>
			<div className="flex items-center gap-2">
				<h2 id={id} className="text-foreground text-[15px] font-semibold tracking-tight">
					{title}
				</h2>
				{count !== undefined && (
					<span className="bg-muted text-muted-foreground rounded-full px-1.5 text-[11px] leading-5 font-medium tabular-nums">{count}</span>
				)}
				{link && (
					<Link
						to={link.to as never}
						className="text-muted-foreground hover:text-foreground focus-visible:ring-ring/60 ml-auto inline-flex items-center gap-1 rounded-sm text-xs font-medium transition-colors focus-visible:ring-2 focus-visible:outline-none"
					>
						{link.label}
						<ArrowRight className="size-3.5" aria-hidden="true" />
					</Link>
				)}
			</div>
			<div className={cn("min-w-0", framed && "bg-card overflow-hidden rounded-lg border shadow-(--shadow-card)")}>{children}</div>
		</section>
	);
}