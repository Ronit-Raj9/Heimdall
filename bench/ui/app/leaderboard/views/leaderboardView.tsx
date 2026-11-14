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
import { LeaderboardSyntheticBanner } from "@/components/leaderboard/leaderboardBanner";
import { LeaderboardFilters } from "@/components/leaderboard/leaderboardFilters";
import { LeaderboardFootnote } from "@/components/leaderboard/leaderboardFootnote";
import { LeaderboardTable } from "@/components/leaderboard/leaderboardTable";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import { useGetLeaderboardQuery } from "@/lib/store";
import type { LeaderboardParams, LeaderboardResponse } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { parseAsString, parseAsStringLiteral, useQueryState } from "nuqs";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

/**
 * The leaderboard: /leaderboard?suite=&condition=&scope=&synthetic=&sort=&dir=. The first four go
 * to the server; an absent one takes the server default, so "newest suite" stays a live default.
 * sort and dir order the rows on the client; the rank is always the board's.
 */
export default function LeaderboardView() {
	const { t } = useTranslation("bench");
	const [suite, setSuite] = useQueryState("suite", parseAsString);
	const [condition, setCondition] = useQueryState("condition", parseAsString);
	const [scope, setScope] = useQueryState("scope", parseAsStringLiteral(LEADERBOARD_SCOPES));
	const [synthetic, setSynthetic] = useQueryState("synthetic", parseAsStringLiteral(LEADERBOARD_SYNTHETIC));

	const params = useMemo<LeaderboardParams>(
		() => ({ suite: suite ?? undefined, condition: condition ?? undefined, scope: scope ?? undefined, synthetic: synthetic ?? undefined }),
		[suite, condition, scope, synthetic],
	);
	const board = useGetLeaderboardQuery(params);
	const hasFilters = Boolean(suite || condition || scope || synthetic);
	const resetFilters = () => {
		void setSuite(null);
		void setCondition(null);
		void setScope(null);
		void setSynthetic(null);
	};

	const data = board.data;
	const showBanner = synthetic === "only" || data?.synthetic === "only" || data?.mode === "synthetic";

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="leaderboard-screen">
			<header className="flex flex-col gap-1">
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("leaderboard.title")}</h1>
					<ModeBadge mode={data?.mode} isLoading={board.isLoading} isError={board.isError && !data} />
				</div>
				<p className="text-muted-foreground max-w-3xl text-sm" data-testid="leaderboard-explainer">
					{t("leaderboard.subtitle")}
				</p>
			</header>

			{showBanner && <LeaderboardSyntheticBanner />}

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
			/>

			{board.isLoading ? (
				<BlockSkeleton rows={6} testId="leaderboard-loading" />
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
		</section>
	);
}

function Board({ data, isFetching, onShowSynthetic }: { data: LeaderboardResponse; isFetching: boolean; onShowSynthetic: () => void }) {
	const { t } = useTranslation("bench");
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
			<Section
				title={t("leaderboard.table.title")}
				description={t("leaderboard.table.description")}
				testId="leaderboard-ranking"
				actions={
					entries.length > 0 && (
						<CopyMarkdownButton build={() => leaderboardMarkdown(data, rows, faults, getLocale())} disabled={isFetching} />
					)
				}
			>
				{entries.length === 0 ? (
					<EmptyBoard data={data} onShowSynthetic={onShowSynthetic} />
				) : (
					<div className="min-w-0 rounded-sm border">
						<LeaderboardTable rows={rows} faults={faults} sort={sort} onSort={onSort} alpha={data.alpha} />
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