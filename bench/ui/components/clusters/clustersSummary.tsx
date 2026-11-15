import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { ClusterList } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

/**
 * The scope line over the table: "N failures in M verified runs", the experiments read, the runs
 * left out by reason, and how many clusters fall below the minimum.
 */
export function ClustersSummary({
	data,
}: {
	data: Pick<ClusterList, "runs" | "failures" | "experiments" | "excluded" | "hidden" | "filters" | "generated_at">;
}) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const excluded = (data.excluded ?? []).filter((group) => group.runs > 0);
	const experiments = data.experiments ?? [];
	const generated = formatDateTime(data.generated_at, locale);

	return (
		<div className="flex flex-col gap-1.5 text-sm" data-testid="clusters-summary">
			<p className="flex flex-wrap items-baseline gap-x-2">
				<span className="text-foreground font-semibold tabular-nums" data-testid="clusters-summary-line">
					{t("clusters.summary.line", {
						failures: t("clusters.summary.failures", { count: data.failures }),
						runs: t("clusters.summary.runs", { count: data.runs }),
					})}
				</span>
				{experiments.length > 0 && (
					<span className="text-muted-foreground text-xs" title={experiments.join(", ")} data-testid="clusters-summary-experiments">
						{t("clusters.summary.experiments", { count: experiments.length })}
					</span>
				)}
				{generated && <span className="text-muted-foreground text-xs">{t("clusters.generatedAt", { at: generated })}</span>}
			</p>
			<div className="text-muted-foreground flex flex-wrap items-baseline gap-x-2 gap-y-1 text-xs" data-testid="clusters-summary-excluded">
				{excluded.length === 0 ? (
					<span>{t("clusters.summary.excludedNone")}</span>
				) : (
					<>
						<span>{t("clusters.summary.excluded")}</span>
						<ul className="flex flex-wrap gap-1.5">
							{excluded.map((group) => (
								<li
									key={group.reason}
									className="bg-muted rounded-sm border px-1.5 py-0.5"
									data-testid={`clusters-excluded-${group.reason.replace(/\s+/g, "-")}`}
								>
									<span className="text-foreground">{group.reason}</span>{" "}
									<span className="tabular-nums">{t("clusters.summary.excludedRuns", { count: group.runs })}</span>
								</li>
							))}
						</ul>
					</>
				)}
			</div>
			{data.hidden > 0 && (
				<p className="text-muted-foreground text-xs" data-testid="clusters-summary-hidden">
					{t("clusters.summary.hidden", { count: data.hidden, min: data.filters?.min ?? 1 })}
				</p>
			)}
		</div>
	);
}