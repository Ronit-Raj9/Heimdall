import { Badge } from "@/components/ui/badge";
import type { JudgeRecord } from "@/lib/types/bench";
import { CheckCircle2, XCircle } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * A custom judge prompt's verdict per criterion: met or not, its weight and the judge's reason, with
 * the weighted score against the share a pass needs. Nothing renders for a judge without criteria.
 * The reasons are the judge model's own words, shown as text.
 */
export function JudgeCriteria({ judge, testId = "judge-criteria" }: { judge?: JudgeRecord | null; testId?: string }) {
	const { t } = useTranslation("runTrace");
	const criteria = judge?.criteria ?? [];
	if (criteria.length === 0) return null;
	const pct = (x: number | undefined) => (x == null ? "-" : `${Math.round(x * 100)}%`);
	return (
		<div className="flex flex-col gap-2" data-testid={testId}>
			<div className="flex flex-wrap items-center gap-2 text-xs">
				<span className="text-foreground font-medium">{t("judgeCriteria.title")}</span>
				<Badge variant="outline" className="tabular-nums" data-testid={`${testId}-score`}>
					{t("judgeCriteria.score", { score: pct(judge?.score), need: pct(judge?.pass_at ?? 1) })}
				</Badge>
			</div>
			<ul className="flex flex-col gap-1.5 text-xs">
				{criteria.map((c) => (
					<li key={c.id} className="flex items-start gap-2" data-testid={`${testId}-${c.id}`} data-met={c.met}>
						{c.met ? (
							<CheckCircle2 className="text-chart-success-ink mt-0.5 size-3.5 shrink-0" aria-label={t("judgeCriteria.met")} />
						) : (
							<XCircle className="text-chart-error-ink mt-0.5 size-3.5 shrink-0" aria-label={t("judgeCriteria.notMet")} />
						)}
						<span className="flex min-w-0 flex-col gap-0.5">
							<span className="text-foreground">
								{c.text}
								<span className="text-muted-foreground ml-1 tabular-nums">{t("judgeCriteria.weight", { weight: c.weight })}</span>
							</span>
							{c.reason && <span className="text-muted-foreground break-words">{c.reason}</span>}
						</span>
					</li>
				))}
			</ul>
		</div>
	);
}
