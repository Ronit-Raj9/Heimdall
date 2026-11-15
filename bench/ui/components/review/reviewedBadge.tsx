import { cn } from "@/lib/utils";
import { UserCheck } from "lucide-react";
import { useTranslation } from "react-i18next";

export interface ReviewedBadgeProps {
	/** Runs whose outcome came from a human review; absent or 0 when none. */
	reviewed?: number;
	/** Of reviewed, those whose outcome the review changed. */
	changed?: number;
	/** The grade source the numbers were read under. */
	gradeSource?: string;
	testId: string;
	className?: string;
}

/**
 * Says how many runs a person decided, beside a verdict or a board. Shown when reviews were applied
 * (grade source reviewed) or counted. The wording says "human-reviewed", never "verified": no verifier
 * backs these outcomes.
 */
export function ReviewedBadge({ reviewed, changed, gradeSource, testId, className }: ReviewedBadgeProps) {
	const { t } = useTranslation("bench");
	const count = reviewed ?? 0;
	if (count === 0 && gradeSource !== "reviewed") return null;
	return (
		<span
			className={cn(
				"inline-flex w-fit items-center gap-1.5 rounded-sm border border-dashed border-amber-500/60 bg-amber-500/10 px-2 py-0.5 text-xs font-medium",
				className,
			)}
			title={t("review.badge.hint")}
			data-testid={testId}
			data-reviewed={count}
			data-changed={changed ?? 0}
		>
			<UserCheck className="size-3.5" aria-hidden="true" />
			{count === 0 ? (
				<span>{t("review.badge.noneApplied")}</span>
			) : (
				<>
					<span>{t("review.badge.reviewed", { count })}</span>
					{(changed ?? 0) > 0 && <span className="text-muted-foreground">({t("review.badge.changed", { count: changed })})</span>}
				</>
			)}
		</span>
	);
}