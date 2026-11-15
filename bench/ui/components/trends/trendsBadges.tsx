import { Badge } from "@/components/ui/badge";
import type { TrendPoint, TrendRegression, TrendStatus } from "@/lib/types/bench";
import { ArrowDownRight, ArrowUpRight, Ban, CircleDashed, Minus, Sparkles } from "lucide-react";
import { useTranslation } from "react-i18next";

const STATUS_VARIANT = {
	regressed: "destructive",
	improved: "success",
	no_change: "secondary",
	cannot_tell: "outline",
	first: "outline",
} as const satisfies Record<TrendStatus, "destructive" | "success" | "secondary" | "outline">;

const STATUS_ICON = {
	regressed: ArrowDownRight,
	improved: ArrowUpRight,
	no_change: Minus,
	cannot_tell: CircleDashed,
	first: Sparkles,
} as const satisfies Record<TrendStatus, typeof Minus>;

/** A point's or lineage's regression status: icon and word, never color alone; the reason is the tooltip. */
export function RegressionBadge({ regression, testId }: { regression: TrendRegression; testId?: string }) {
	const { t } = useTranslation("bench");
	const status: TrendStatus = regression.status in STATUS_VARIANT ? regression.status : "cannot_tell";
	const Icon = STATUS_ICON[status];
	return (
		<Badge variant={STATUS_VARIANT[status]} title={regression.reason || undefined} data-testid={testId} data-status={status}>
			<Icon aria-hidden="true" />
			{t(`trends.status.${status}`)}
		</Badge>
	);
}

/** The release flag of a point's experiment: regressed or blocked, or nothing. */
export function ReleaseFlag({ point, testId }: { point: Pick<TrendPoint, "release">; testId?: string }) {
	const { t } = useTranslation("bench");
	if (point.release !== "regressed" && point.release !== "blocked") return null;
	return (
		<Badge variant="destructive" data-testid={testId} data-release={point.release}>
			<Ban aria-hidden="true" />
			{t(`trends.release.${point.release}`)}
		</Badge>
	);
}