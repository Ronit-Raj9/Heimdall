import { CopyMarkdownButton } from "@/components/leaderboard/copyMarkdownButton";
import {
	LEADERBOARD_SCOPES,
	LEADERBOARD_SYNTHETIC,
	SORT_DIRS,
	leaderboardMarkdown,
	nextSort,
	parseSort,
	resilienceConditions,
	sortEntries,
	type SortKey,
} from "@/components/leaderboard/leaderboard.utils";
import {
	HeldOutBoardOff,
	HeldOutBoardToggle,
	LeaderboardHeldOutBanner,
	heldOutBoardOffered,
	isNotFoundError,
} from "@/components/leaderboard/heldOutBoard";
import { LeaderboardSyntheticBanner } from "@/components/leaderboard/leaderboardBanner";
import { LeaderboardFilters } from "@/components/leaderboard/leaderboardFilters";
import { LeaderboardFootnote } from "@/components/leaderboard/leaderboardFootnote";
import { LeaderboardTable } from "@/components/leaderboard/leaderboardTable";
import { Podium } from "@/components/leaderboard/podium";
import { ShareButton, shareQuery } from "@/components/share/shareDialog";
import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { GRADE_SOURCE_MODES, GradeSourcePicker } from "@/components/review/gradeSourcePicker";
import { ReviewedBadge } from "@/components/review/reviewedBadge";
import { GradingBadge, JudgedNotice } from "@/components/scenario/scenarioBadges";
import { groupSuitesByTrack, isJudgedSuite, suiteFactsById } from "@/components/scenario/scenario.utils";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import { useGetLeaderboardQuery, useGetSuitesQuery } from "@/lib/store";
import type { LeaderboardParams, LeaderboardResponse } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { parseAsString, parseAsStringLiteral, useQueryState } from "nuqs";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { LeaderboardParetoPanel } from "@/components/migration/migrationReport"; // W6 cost against verified success
import TrendsTab from "./trendsTab";
import { AgentBoardTab } from "@/components/leaderboard/agentBoard"; // A17: agent leaderboard, the default tab
import { ReferenceBoardPanel } from "@/components/reference/referenceBoard"; // G4-03: the reference board
import "@/lib/types/referenceI18n";

/**
 * Leaderboard: /leaderboard?tab=. Agents (default: one row per agent version and model), the
 * per-benchmark board ("By benchmark") and the Trends tab (the old /trends); the last two share
 * the suite, condition and synthetic filters in the URL.
 */
export default function LeaderboardView() {
	const { t } = useTranslation("bench");
	const { t: ta } = useTranslation("agentBoard");
	const { t: tr } = useTranslation("reference");
	// Links into one benchmark's board (?suite=) still land on it, not on Agents.
	const [suiteParam] = useQueryState("suite", parseAsString);
	const tabs: RouteTab[] = [
		{ id: "agents", label: ta("tabs.agents") },
		{ id: "leaderboard", label: ta("tabs.byBenchmark") },
		{ id: "trends", label: t("leaderboard.tabs.trends") },
		{ id: "reference", label: tr("tab") }, // G4-03: scores of known agents from the Heimdall team's published runs
	];
	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-6" data-testid="leaderboard-page">
			<PageHeader title={t("leaderboard.title")} subtitle={t("leaderboard.pageSubtitle")} actions={<LeaderboardShare />} />
			<RouteTabs tabs={tabs} defaultTab={suiteParam ? "leaderboard" : "agents"} label={t("leaderboard.tabs.label")} testId="leaderboard-tabs">
				{(active) =>
					active === "trends" ? <TrendsTab /> : active === "leaderboard" ? <LeaderboardTab /> : active === "reference" ? <ReferenceTab /> : <AgentBoardTab />
				}
			</RouteTabs>
		</section>
	);
}

/**
 * Share the board in view (lane X14): the Agents tab with its skill and model filters, or one
 * benchmark's board with its filters. The held-out board and Trends are never shared.
 */
function LeaderboardShare() {
	const [tab] = useQueryState("tab", parseAsString);
	const [suite] = useQueryState("suite", parseAsString);
	const [condition] = useQueryState("condition", parseAsString);
	const [scope] = useQueryState("scope", parseAsString);
	const [synthetic] = useQueryState("synthetic", parseAsString);
	const [gradeSource] = useQueryState("grade_source", parseAsString);
	const [split] = useQueryState("split", parseAsString);
	const [capability] = useQueryState("capability", parseAsString);
	const [model] = useQueryState("model", parseAsString);
	const active = tab ?? (suite ? "leaderboard" : "agents");
	if (active === "trends" || active === "reference" || split) return null;
	if (active === "agents") return <ShareButton kind="leaderboard" target="agents" query={shareQuery({ capability, model })} />;
	return <ShareButton kind="leaderboard" target="models" query={shareQuery({ suite, condition, scope, synthetic, grade_source: gradeSource })} />;
}

/**
 * The board tab: ?track=&suite=&condition=&scope=&synthetic=&sort=&dir=. suite,
 * condition, scope and synthetic go to the server; an absent one takes the server default, so
 * "newest suite" stays a live default. track narrows the suite picker to one track, from the
 * suites on disk; sort and dir order the rows on the client; the rank is always the board's.
 */
export function LeaderboardTab() {
	const { t } = useTranslation("bench");
	const [suite, setSuite] = useQueryState("suite", parseAsString);
	const [condition, setCondition] = useQueryState("condition", parseAsString);
	const [scope, setScope] = useQueryState("scope", parseAsStringLiteral(LEADERBOARD_SCOPES));
	const [synthetic, setSynthetic] = useQueryState("synthetic", parseAsStringLiteral(LEADERBOARD_SYNTHETIC));
	const [track, setTrack] = useQueryState("track", parseAsString);
	const [gradeSource, setGradeSource] = useQueryState("grade_source", parseAsStringLiteral(GRADE_SOURCE_MODES));
	// held_out shows the separate held-out board; absent is the public board (suite health area).
	const [split, setSplit] = useQueryState("split", parseAsStringLiteral(["held_out"] as const));
	// Track and grading come from the suites on disk; the board itself carries neither.
	const suitesQuery = useGetSuitesQuery();
	const facts = useMemo(() => suiteFactsById(suitesQuery.data?.suites ?? []), [suitesQuery.data]);
	// U3: the held-out board is served only with held_out.visible; offer it only when some suite has held-out tasks the server shows.
	const heldOutBoard = useMemo(() => heldOutBoardOffered(suitesQuery.data?.suites ?? []), [suitesQuery.data]);

	const params = useMemo<LeaderboardParams>(
		() => ({
			suite: suite ?? undefined,
			condition: condition ?? undefined,
			scope: scope ?? undefined,
			synthetic: synthetic ?? undefined,
			grade_source: gradeSource ?? undefined,
			split: split ?? undefined,
		}),
		[suite, condition, scope, synthetic, gradeSource, split],
	);
	const board = useGetLeaderboardQuery(params);
	const hasFilters = Boolean(suite || condition || scope || synthetic || track || gradeSource || split);
	const resetFilters = () => {
		void setSplit(null);
		void setTrack(null);
		void setSuite(null);
		void setCondition(null);
		void setScope(null);
		void setSynthetic(null);
		void setGradeSource(null);
	};

	const data = board.data;
	const showBanner = synthetic === "only" || data?.synthetic === "only" || data?.mode === "synthetic";
	const trackGroups = useMemo(() => groupSuitesByTrack(data?.suites ?? [], facts), [data?.suites, facts]);
	// The board's own grading provenance wins; today's suite catalog is only a fallback for older servers.
	const judged = data?.grading ? data.grading === "judged" || data.grading === "mixed" : isJudgedSuite(data?.suite, facts);
	const onTrack = (next: string | null) => {
		void setTrack(next);
		// A board outside the chosen track moves to that track's newest suite.
		const group = trackGroups.find((g) => g.track === next);
		if (group && data && !group.suites.includes(data.suite)) {
			void setSuite(group.suites[0]);
			void setCondition(null);
		}
	};

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="leaderboard-screen">
			<div className="flex flex-wrap items-center gap-x-3 gap-y-2">
				<p className="text-muted-foreground max-w-3xl text-sm" data-testid="leaderboard-explainer">
					{t("leaderboard.subtitle")}
				</p>
				{judged && <GradingBadge grading="judged" testId="leaderboard-judged-badge" />}
				<ReviewedBadge
					reviewed={data?.reviewed_runs}
					changed={data?.review_changed_runs}
					gradeSource={data?.grade_source}
					testId="leaderboard-reviewed-badge"
				/>
			</div>
			{showBanner && <LeaderboardSyntheticBanner />}
			{data?.split === "held_out" && <LeaderboardHeldOutBanner />}
			{judged && (
				<JudgedNotice title={t("leaderboard.judged.title")} body={t("leaderboard.judged.body")} testId="leaderboard-judged-note" />
			)}
			<LeaderboardFilters
				suite={data?.suite ?? suite ?? ""}
				suites={data?.suites ?? []}
				condition={data?.condition ?? condition ?? ""}
				conditions={data?.conditions ?? []}
				scope={data?.scope ?? scope ?? "all"}
				synthetic={data?.synthetic ?? synthetic ?? "exclude"}
				onSuite={(next) => {
					// Conditions differ between suites; the new suite starts on its default condition.
					void setSuite(next);
					void setCondition(null);
				}}
				onCondition={(next) => void setCondition(next)}
				onScope={(next) => void setScope(next)}
				onSynthetic={(next) => void setSynthetic(next)}
				disabled={board.isLoading}
				trackGroups={trackGroups}
				track={track}
				onTrack={onTrack}
			/>
			<GradeSourcePicker
				value={data?.grade_source ?? gradeSource ?? "original"}
				onChange={(next) => void setGradeSource(next === "original" ? null : next)}
				disabled={board.isLoading}
				testId="leaderboard-grade-source"
			/>
			{(heldOutBoard || split === "held_out") && (
				<HeldOutBoardToggle
					heldOut={split === "held_out"}
					onChange={(next) => void setSplit(next ? "held_out" : null)}
					disabled={board.isLoading}
				/>
			)}
			{board.isLoading ? (
				<BlockSkeleton rows={6} testId="leaderboard-loading" />
			) : split === "held_out" && isNotFoundError(board.error) ? (
				<HeldOutBoardOff onBack={() => void setSplit(null)} />
			) : board.isError || !data ? (
				<div className="flex flex-col gap-2">
					<LoadError
						title={t("leaderboard.loadError")}
						error={board.error}
						onRetry={() => void board.refetch()}
						testId="leaderboard-error"
					/>
					{hasFilters && (
						<Button variant="outline" size="sm" className="self-start" onClick={resetFilters} dataTestId="leaderboard-reset-filters">
							{t("leaderboard.resetFilters")}
						</Button>
					)}
				</div>
			) : (
				<Board data={data} isFetching={board.isFetching} onShowSynthetic={() => void setSynthetic("only")} />
			)}
			{data && data.entries.length > 0 && <LeaderboardParetoPanel params={params} />} {/* W6 cost against verified success */}
		</section>
	);
}

function Board({ data, isFetching, onShowSynthetic }: { data: LeaderboardResponse; isFetching: boolean; onShowSynthetic: () => void }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u12");
	const [compact, setCompact] = useState(true);
	const [sortKey, setSortKey] = useQueryState("sort", parseAsString);
	const [sortDir, setSortDir] = useQueryState("dir", parseAsStringLiteral(SORT_DIRS));

	const entries = useMemo(() => data.entries ?? [], [data.entries]);
	const faults = useMemo(() => (data.condition === "clean" ? resilienceConditions(data) : []), [data]);
	const sort = useMemo(() => parseSort(sortKey, sortDir, faults), [sortKey, sortDir, faults]);
	const rows = useMemo(() => sortEntries(entries, sort), [entries, sort]);

	const onSort = (key: SortKey) => {
		const next = nextSort(sort, key);
		const isDefault = next.key === "rank" && next.dir === "asc";
		void setSortKey(isDefault ? null : next.key);
		void setSortDir(isDefault ? null : next.dir);
	};

	return (
		<div className={cn("flex min-w-0 flex-col gap-6 transition-opacity", isFetching && "opacity-60")} aria-busy={isFetching}>
			{entries.length > 0 && <Podium entries={entries} />}
			<Section
				title={t("leaderboard.table.title")}
				description={t("leaderboard.table.description")}
				testId="leaderboard-ranking"
				actions={
					entries.length > 0 && (
						<div className="flex items-center gap-2">
							<Button variant="ghost" size="sm" onClick={() => setCompact((c) => !c)} dataTestId="leaderboard-columns-toggle">
								{compact ? tu("board.more") : tu("board.less")}
							</Button>
							<CopyMarkdownButton build={() => leaderboardMarkdown(data, rows, faults, getLocale())} disabled={isFetching} />
						</div>
					)
				}
			>
				{entries.length === 0 ? (
					<EmptyBoard data={data} onShowSynthetic={onShowSynthetic} />
				) : (
					<div className="bg-card min-w-0 overflow-hidden rounded-xl border">
						<LeaderboardTable rows={rows} faults={faults} sort={sort} onSort={onSort} alpha={data.alpha} compact={compact} />
					</div>
				)}
			</Section>
			<LeaderboardFootnote board={data} hasResilience={faults.length > 0} />
		</div>
	);
}

/** Says why the board is empty; on a live-only board, offers the synthetic runs that may exist. */
function EmptyBoard({ data, onShowSynthetic }: { data: LeaderboardResponse; onShowSynthetic: () => void }) {
	const { t } = useTranslation("bench");
	if (data.synthetic === "exclude") {
		return (
			<div className="flex flex-col items-center gap-3 rounded-sm border border-dashed px-4 py-6" data-testid="leaderboard-empty-live">
				<p className="text-muted-foreground text-center text-sm">{t("leaderboard.emptyLive")}</p>
				<Button variant="outline" size="sm" onClick={onShowSynthetic} dataTestId="leaderboard-show-synthetic">
					{t("leaderboard.showSynthetic")}
				</Button>
			</div>
		);
	}
	if ((data.suites ?? []).length === 0) return <EmptyNote testId="leaderboard-no-runs">{t("leaderboard.noRuns")}</EmptyNote>;
	return <EmptyNote testId="leaderboard-empty">{t("leaderboard.empty")}</EmptyNote>;
}
/** The Reference tab: known agents on the built-in benchmarks, from the published reference runs. */
function ReferenceTab() {
	const { t } = useTranslation("reference");
	return (
		<section className="flex min-w-0 flex-col gap-4" data-testid="reference-tab">
			<div className="flex flex-wrap items-baseline justify-between gap-2">
				<p className="text-muted-foreground max-w-3xl text-sm">{t("subtitle")}</p>
				<a className="text-primary text-sm hover:underline" href="/reference" data-testid="reference-public-link">
					{t("publicLink")}
				</a>
			</div>
			<ReferenceBoardPanel />
		</section>
	);
}
