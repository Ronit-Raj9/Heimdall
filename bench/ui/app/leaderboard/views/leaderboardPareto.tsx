import { ParetoChart } from "@/components/migration/paretoChart";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { useGetLeaderboardParetoQuery } from "@/lib/store/apis/migrationApi";
import type { LeaderboardParetoParams, Pareto } from "@/lib/types/bench";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

/** A short, unique name for a config hash: the agent and the first 6 characters of the hash. */
export function paretoShortIds<T extends Pareto>(pareto: T): T {
	const short = new Map(pareto.points.map((p) => [p.id, p.agent ? `${p.agent} ${p.id.slice(0, 6)}` : p.id.slice(0, 12)]));
	// Two hashes that share the first 6 characters keep their full id, so names never collide.
	const seen = new Map<string, number>();
	for (const name of short.values()) seen.set(name, (seen.get(name) ?? 0) + 1);
	const name = (id: string) => {
		const s = short.get(id);
		return s && seen.get(s) === 1 ? s : id;
	};
	return {
		...pareto,
		frontier: pareto.frontier.map(name),
		points: pareto.points.map((p) => ({ ...p, id: name(p.id), dominated_by: p.dominated_by.map(name) })),
	};
}

/**
 * Cost against verified success for the board (the shared chart), with config hashes shortened:
 * 64-character hashes as point labels ran into each other and filled the legend.
 */
export function LeaderboardPareto({ params }: { params: LeaderboardParetoParams }) {
	const { t } = useTranslation("bench");
	const query = useGetLeaderboardParetoQuery(params);
	const data = useMemo(() => (query.data ? paretoShortIds(query.data) : undefined), [query.data]);
	return (
		<section className="bg-card flex flex-col gap-3 rounded-xl border p-4" data-testid="leaderboard-pareto-panel">
			<h3 className="text-foreground text-base font-semibold">{t("migration.pareto.title")}</h3>
			{query.isLoading ? (
				<BlockSkeleton rows={4} testId="leaderboard-pareto-loading" />
			) : query.error || !data ? (
				<LoadError
					title={t("migration.pareto.loadError")}
					error={query.error}
					onRetry={() => void query.refetch()}
					testId="leaderboard-pareto-error"
				/>
			) : (
				<ParetoChart pareto={data} testId="leaderboard-pareto" />
			)}
		</section>
	);
}
