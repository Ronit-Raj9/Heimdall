import { EntityLink } from "@/components/shell";
import type { SuiteSummary, SuiteTier } from "@/lib/types/bench";
import { CircleX } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ToneBadge } from "./suiteBadges";

/** Built-in tier names with a plain-words purpose (docs/suite-tiers.md). */
const KNOWN_TIERS = ["pr", "nightly", "headroom"] as const;
type KnownTier = (typeof KNOWN_TIERS)[number];

function isKnownTier(name: string): name is KnownTier {
	return (KNOWN_TIERS as readonly string[]).includes(name);
}

/** The tiers of a suite, nightly (every task) first when the suite defines none. */
export function suiteTiers(suite: Pick<SuiteSummary, "tiers" | "tasks">): SuiteTier[] {
	if (suite.tiers && suite.tiers.length > 0) return suite.tiers;
	// An older server sends no tiers: nightly still means every task.
	return [{ name: "nightly", tasks: suite.tasks.map((task) => task.id), defined: false }];
}

/** One card per tier: its purpose, how many tasks, and each task linked to its row. */
export function SuiteTiers({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	const tiers = suiteTiers(suite);
	const definedCount = tiers.filter((tier) => tier.defined).length;
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="suite-tiers">
			{definedCount === 0 && (
				<p className="text-muted-foreground text-sm" data-testid="suite-tiers-none">
					{t("suites.tiers.none")}
				</p>
			)}
			<ul className="grid min-w-0 grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
				{tiers.map((tier) => (
					<li key={tier.name} className="bg-card flex min-w-0 flex-col gap-2 rounded-sm border p-3" data-testid={`suite-tier-${tier.name}`}>
						<div className="flex min-w-0 flex-wrap items-center gap-2">
							<h3 className="text-foreground font-mono text-sm font-semibold">{tier.name}</h3>
							<ToneBadge tone="neutral">{t("suites.tiers.tasks", { count: tier.tasks.length })}</ToneBadge>
							{!tier.defined && <ToneBadge tone="neutral">{t("suites.tiers.implicit")}</ToneBadge>}
						</div>
						{isKnownTier(tier.name) && <p className="text-muted-foreground text-xs">{t(`suites.tiers.purpose.${tier.name}`)}</p>}
						{tier.error ? (
							<p
								className="text-chart-error-ink flex items-start gap-1.5 text-xs"
								role="alert"
								data-testid={`suite-tier-${tier.name}-error`}
							>
								<CircleX className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
								{t("suites.tiers.error", { error: tier.error })}
							</p>
						) : (
							<ul className="flex min-w-0 flex-wrap gap-x-2 gap-y-1 text-xs">
								{tier.tasks.map((task) => (
									<li key={task}>
										<EntityLink kind="task" id={task} suite={suite.id} />
									</li>
								))}
							</ul>
						)}
					</li>
				))}
			</ul>
		</div>
	);
}