import { cn } from "@/lib/utils";
import { BadgeCheck, Gavel, RefreshCw, UserCheck, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * Where a block's grades come from (UI regroup, U2). Every number built from grades says which:
 *   verified  the verifier's grade the run was sealed with (hidden tests, state checks)
 *   judged    the sealed grade of a judged suite (an LLM judge, calibrated or not)
 *   regraded  a regrade of the sealed runs; the id names which
 *   reviewed  human reviews where they exist, else the sealed grade
 */
export type GradeSourceKind = "verified" | "judged" | "regraded" | "reviewed";

/** The kind of a grade source as the server names it ("original", a regrade id, or "reviewed"). */
export function gradeSourceKind(gradeSource: string | null | undefined, judged = false): GradeSourceKind {
	if (!gradeSource || gradeSource === "original") return judged ? "judged" : "verified";
	if (gradeSource === "reviewed") return "reviewed";
	return "regraded";
}

const ICON: Record<GradeSourceKind, LucideIcon> = {
	verified: BadgeCheck,
	judged: Gavel,
	regraded: RefreshCw,
	reviewed: UserCheck,
};

const TONE: Record<GradeSourceKind, string> = {
	verified: "border-chart-success/40 text-chart-success-ink",
	judged: "border-chart-warning/50 text-chart-warning-ink",
	regraded: "border-chart-info/50 text-chart-info-ink",
	reviewed: "border-chart-info/50 text-chart-info-ink",
};

export interface GradeSourceTagProps {
	/** The server's grade source: "original" (or absent), a regrade id, or "reviewed". */
	source?: string | null;
	/** The suite is judged, so original grades are a judge's, not a verifier's. */
	judged?: boolean;
	/** Sealed outcomes only: the block never reads a regrade (the task grid, gates, the run list). */
	sealedOnly?: boolean;
	className?: string;
	testId?: string;
}

/** A small label naming the grades a block is built from, with the regrade id when there is one. */
export function GradeSourceTag({ source, judged = false, sealedOnly = false, className, testId }: GradeSourceTagProps) {
	const { t } = useTranslation("bench");
	const kind = gradeSourceKind(sealedOnly ? null : source, judged);
	const Icon = ICON[kind];
	return (
		<span
			className={cn(
				"bg-background inline-flex h-5 shrink-0 items-center gap-1 rounded-[5px] border px-1.5 text-[11px] leading-4 font-medium whitespace-nowrap",
				TONE[kind],
				className,
			)}
			title={t(`verdict.source.hint.${kind}`)}
			data-testid={testId}
			data-grade-source={kind}
		>
			<Icon className="size-3" aria-hidden="true" />
			{kind === "regraded" ? t("verdict.source.regradedId", { id: source }) : t(`verdict.source.kind.${kind}`)}
		</span>
	);
}