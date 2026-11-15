import { ModeBadge } from "@/components/modeBadge";
import { ReviewedBadge } from "@/components/review/reviewedBadge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { formatDateTime, shortHash } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { VerdictResponse } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

export interface VerdictHeaderStripProps {
	payload: VerdictResponse;
}

/** Mode, manifest hash and frozen time, suite and grade source (DESIGN.md 9.2, 18). */
export function VerdictHeaderStrip({ payload }: VerdictHeaderStripProps) {
	const { t } = useTranslation("bench");
	const { verdict } = payload;
	const frozen = formatDateTime(verdict.frozen_at, getLocale());
	const gradeSource = payload.grade_source === "original" ? t("verdict.header.gradeSourceOriginal") : payload.grade_source;

	return (
		<div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm" data-testid="verdict-header-strip">
			<ModeBadge mode={payload.mode} />
			<span className="flex items-center gap-1.5">
				<span className="text-muted-foreground">{t("verdict.header.manifest")}</span>
				<Tooltip>
					<TooltipTrigger asChild>
						<button
							type="button"
							className="hover:bg-accent focus-visible:ring-ring/50 cursor-help rounded-sm px-1 font-mono text-xs focus-visible:ring-[3px]"
							aria-label={t("verdict.header.manifestHint", { hash: verdict.manifest_hash })}
							data-testid="verdict-manifest-hash"
						>
							{shortHash(verdict.manifest_hash)}
						</button>
					</TooltipTrigger>
					<TooltipContent className="max-w-md font-mono break-all">
						{t("verdict.header.manifestHint", { hash: verdict.manifest_hash })}
					</TooltipContent>
				</Tooltip>
				{frozen && (
					<span className="text-muted-foreground" data-testid="verdict-frozen-at">
						{t("verdict.header.frozen", { time: frozen })}
					</span>
				)}
			</span>
			<span className="flex items-center gap-1.5" data-testid="verdict-suite">
				<span className="text-muted-foreground">{t("verdict.header.suite")}</span>
				<span className="font-mono text-xs">{payload.suite}</span>
			</span>
			<span className="flex items-center gap-1.5" data-testid="verdict-grade-source" title={t("verdict.header.gradeSourceHint")}>
				<span className="text-muted-foreground">{t("verdict.header.gradeSource")}</span>
				<span className="font-mono text-xs">{gradeSource}</span>
			</span>
			<ReviewedBadge
				reviewed={payload.reviewed_runs}
				changed={payload.review_changed_runs}
				gradeSource={payload.grade_source}
				testId="verdict-reviewed-badge"
			/>
		</div>
	);
}