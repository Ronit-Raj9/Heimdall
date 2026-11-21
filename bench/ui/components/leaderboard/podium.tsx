import { ValueCell } from "@/components/valueCell";
import { formatValueNumber } from "@/components/valueCell.utils";
import { getLocale } from "@/lib/i18n";
import type { LeaderboardEntry } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Award, Medal, Trophy } from "lucide-react";
import { useTranslation } from "react-i18next";
import { observedNumber } from "./leaderboard.utils";

const PLACE = [
	{ icon: Trophy, ring: "border-amber-400/60", tint: "bg-amber-400/10 text-amber-600 dark:text-amber-400", bar: "bg-amber-400" },
	{ icon: Medal, ring: "border-slate-400/60", tint: "bg-slate-400/10 text-slate-600 dark:text-slate-300", bar: "bg-slate-400" },
	{ icon: Award, ring: "border-orange-400/50", tint: "bg-orange-400/10 text-orange-700 dark:text-orange-400", bar: "bg-orange-400" },
] as const;

/** The top three agents by rank as cards: place, agent and model, pass rate bar, cost per success. */
export function Podium({ entries }: { entries: readonly LeaderboardEntry[] }) {
	const { t } = useTranslation("u12");
	const locale = getLocale();
	const top = [...entries].sort((a, b) => a.rank - b.rank).slice(0, 3);
	if (top.length === 0) return null;
	return (
		<section aria-label={t("board.podium")} className="grid grid-cols-1 gap-4 sm:grid-cols-3" data-testid="leaderboard-podium">
			{top.map((entry, i) => {
				const place = PLACE[i];
				const Icon = place.icon;
				const rate = observedNumber(entry.success);
				const fill = rate === null ? 0 : Math.max(0, Math.min(1, rate));
				return (
					<article
						key={entry.config_hash}
						className={cn(
							"bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5 transition-shadow hover:shadow-md",
							i === 0 && place.ring,
							i === 0 && "sm:-translate-y-1",
						)}
						data-testid={`podium-${entry.rank}`}
					>
						<div className="flex items-center justify-between gap-2">
							<span className={cn("inline-flex size-9 items-center justify-center rounded-full", place.tint)}>
								<Icon className="size-4.5" aria-hidden="true" />
							</span>
							<span className="text-muted-foreground text-sm font-semibold tabular-nums">{t("board.place", { rank: entry.rank })}</span>
						</div>
						<div className="min-w-0">
							<Link
								to="/agents/$agentId"
								params={{ agentId: entry.agent } as never}
								className="hover:text-primary block truncate text-base font-semibold tracking-tight"
								title={entry.agent}
							>
								{entry.label || entry.agent}
							</Link>
							<div className="text-muted-foreground truncate font-mono text-xs" title={entry.model}>
								{entry.model}
							</div>
						</div>
						<div className="flex flex-col gap-1.5">
							<div className="flex items-baseline justify-between">
								<span className="text-2xl font-semibold tracking-tight tabular-nums">
									{rate === null ? "-" : formatValueNumber(rate, "rate", locale)}
								</span>
								<span className="text-muted-foreground text-xs">{t("board.passRate")}</span>
							</div>
							<div className="bg-muted h-1.5 overflow-hidden rounded-full">
								<div className={cn("h-full rounded-full", place.bar)} style={{ width: `${fill * 100}%` }} />
							</div>
						</div>
						<div className="text-muted-foreground flex items-center justify-between text-xs">
							<span className="inline-flex items-center gap-1">
								<ValueCell value={entry.metrics?.cost_per_success} kind="usd" />
								<span>{t("board.perSuccess")}</span>
							</span>
							<span className="tabular-nums">{t("board.runs", { count: entry.runs })}</span>
						</div>
					</article>
				);
			})}
		</section>
	);
}
