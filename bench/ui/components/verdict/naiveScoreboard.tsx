import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ValueCell } from "@/components/valueCell";
import { getLocale } from "@/lib/i18n";
import type { Ratio } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { formatCount, naiveLeader } from "./verdict.utils";

export interface NaiveScoreboardProps {
	/** arm -> raw passes over runs. */
	naive: Record<string, Ratio>;
	/** Arm A then arm B. */
	arms: string[];
	className?: string;
}

/**
 * Raw passes per arm and "B wins by N", exactly as a typical dashboard would show them. It sits
 * beside the primary verdict so the difference between the two readings is visible (DESIGN.md 18).
 */
export function NaiveScoreboard({ naive, arms, className }: NaiveScoreboardProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const { leader, by } = naiveLeader(naive, arms);

	return (
		<Card className={cn("gap-4", className)} data-testid="verdict-naive-scoreboard">
			<CardHeader>
				<CardTitle>{t("verdict.naive.title")}</CardTitle>
				<CardDescription>{t("verdict.naive.caption")}</CardDescription>
			</CardHeader>
			<CardContent className="flex flex-col gap-4">
				<dl className="flex flex-wrap gap-x-8 gap-y-2">
					{arms.map((arm) => {
						const ratio = naive[arm];
						return (
							<div key={arm} className="flex items-baseline gap-2" data-testid={`verdict-naive-arm-${arm}`}>
								<dt className="text-muted-foreground font-mono text-sm">{arm}</dt>
								<dd className="text-2xl font-semibold tabular-nums">
									{ratio ? (
										t("verdict.naive.ratio", { num: formatCount(ratio.num, locale), den: formatCount(ratio.den, locale) })
									) : (
										<ValueCell value={undefined} kind="count" testId={`verdict-naive-missing-${arm}`} />
									)}
									{ratio && ratio.unknown > 0 && (
										<span className="bench-hatched text-muted-foreground ml-2 rounded-sm border border-dashed px-1 text-xs font-normal">
											{t("verdict.naive.unknown", { count: ratio.unknown })}
										</span>
									)}
								</dd>
							</div>
						);
					})}
				</dl>
				<p className="text-lg font-medium" data-testid="verdict-naive-headline">
					{leader === null ? t("verdict.naive.tie") : t("verdict.naive.wins", { arm: leader, count: by })}
				</p>
				<p className="text-muted-foreground text-xs">{t("verdict.naive.notVerdict")}</p>
			</CardContent>
		</Card>
	);
}