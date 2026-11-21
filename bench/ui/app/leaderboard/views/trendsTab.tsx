import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { TREND_METRICS, TREND_SYNTHETIC } from "@/components/trends/trends.utils";
import { TrendsBoard } from "@/components/trends/trendsBoard";
import { TrendsFilters } from "@/components/trends/trendsFilters";
import { Button } from "@/components/ui/button";
import { useGetTrendsQuery } from "@/lib/store/apis/trendsApi";
import type { TrendsParams } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { parseAsString, parseAsStringLiteral, useQueryState } from "nuqs";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

/**
 * The Trends tab of /leaderboard: ?tab=trends&suite=&agent=&metric=&condition=&synthetic= (the old
 * /trends redirects here with its filters). Every filter goes to the server;
 * an absent one takes the server default (every suite, every agent, verified_success, clean,
 * exclude). The numbers come from the derived results index: a monitoring signal that never
 * gates a release.
 */
export default function TrendsTab() {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u12");
	const [suite, setSuite] = useQueryState("suite", parseAsString);
	const [agent, setAgent] = useQueryState("agent", parseAsString);
	const [metric, setMetric] = useQueryState("metric", parseAsStringLiteral(TREND_METRICS));
	const [condition, setCondition] = useQueryState("condition", parseAsString);
	const [synthetic, setSynthetic] = useQueryState("synthetic", parseAsStringLiteral(TREND_SYNTHETIC));

	const params = useMemo<TrendsParams>(
		() => ({
			suite: suite ?? undefined,
			agent: agent ?? undefined,
			metric: metric ?? undefined,
			condition: condition ?? undefined,
			synthetic: synthetic ?? undefined,
		}),
		[suite, agent, metric, condition, synthetic],
	);
	const trends = useGetTrendsQuery(params);
	const data = trends.data;
	const hasFilters = Boolean(suite || agent || metric || condition || synthetic);
	const resetFilters = () => {
		void setSuite(null);
		void setAgent(null);
		void setMetric(null);
		void setCondition(null);
		void setSynthetic(null);
	};

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="trends-screen">
			<div className="flex flex-wrap items-center gap-3">
				<p className="text-muted-foreground max-w-3xl text-sm" data-testid="trends-explainer">
					{t("trends.subtitle")}
				</p>
			</div>

			<TrendsFilters
				suite={data?.suite ?? suite ?? ""}
				suites={data?.suites ?? []}
				agent={data?.agent ?? agent ?? ""}
				agents={data?.agents ?? []}
				metric={data?.metric ?? metric ?? "verified_success"}
				metrics={data?.metrics ?? TREND_METRICS}
				condition={data?.condition ?? condition ?? ""}
				conditions={data?.conditions ?? []}
				synthetic={data?.synthetic ?? synthetic ?? "exclude"}
				onSuite={(next) => void setSuite(next || null)}
				onAgent={(next) => void setAgent(next || null)}
				onMetric={(next) => void setMetric(next)}
				onCondition={(next) => void setCondition(next)}
				onSynthetic={(next) => void setSynthetic(next)}
				disabled={trends.isLoading}
			/>

			{trends.isLoading ? (
				<BlockSkeleton rows={6} testId="trends-loading" />
			) : trends.isError || !data ? (
				<div className="flex flex-col gap-2">
					<LoadError title={t("trends.loadError")} error={trends.error} onRetry={() => void trends.refetch()} testId="trends-error" />
					{hasFilters && (
						<Button variant="outline" size="sm" className="self-start" onClick={resetFilters} dataTestId="trends-reset-filters">
							{t("trends.resetFilters")}
						</Button>
					)}
				</div>
			) : (
				<div
					className={cn("bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5 transition-opacity", trends.isFetching && "opacity-60")}
					aria-busy={trends.isFetching}
				>
					<header className="flex flex-col gap-0.5">
						<h2 className="text-base font-semibold tracking-tight">{tu("trends.title")}</h2>
						<p className="text-muted-foreground text-sm">{tu("trends.subtitle")}</p>
					</header>
					<TrendsBoard data={data} onIncludeSynthetic={() => void setSynthetic("include")} />
				</div>
			)}
		</section>
	);
}