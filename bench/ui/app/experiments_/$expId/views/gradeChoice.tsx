import { formatDateTime } from "@/components/verdict/verdict.utils";
import { useGetExperimentQuery } from "@/lib/store/apis/benchApi";
import type { RegradeRef } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { RefreshCw } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import "./hx01I18n";

/** The ?grade= value that asks for the original result even when a re-score exists. */
export const ORIGINAL_GRADE = "original";

/**
 * The grade source the comparison page reads: the one ?grade= names, the original for ?grade=original,
 * and otherwise the newest committed re-score from the status (null, the original, when there is none).
 * The server's verdict route keeps answering the original unless asked, so the page asks. Pure.
 */
export function effectiveGrade(param: string | null | undefined, latest: RegradeRef | undefined): string | null {
	if (param === ORIGINAL_GRADE) return null;
	if (param) return param;
	return latest?.id ?? null;
}

/**
 * The verdict or diagnostics request for the grade the page shows. The server's default is the newest
 * re-score (wave 4, N200), so the original (null) is asked for by name. Pure.
 */
export function gradeQuery(expId: string, grade: string | null): { expId: string; gradeSource: string } {
	return { expId, gradeSource: grade ?? ORIGINAL_GRADE };
}

/** The ?grade= value for a grade source picked on the page: "original" (or null) asks for the original. Pure. */
export function gradeParam(next: string | null, latest: RegradeRef | undefined): string | null {
	if (next === null || next === ORIGINAL_GRADE) return latest ? ORIGINAL_GRADE : null;
	return next;
}

/** The comparison page's grade source and its newest re-score, shared by the header and the tabs. */
export function useGradeChoice(expId: string) {
	const [param, setParam] = useQueryState("grade", parseAsString);
	const status = useGetExperimentQuery(expId);
	const latest = status.data && status.data.exp_id === expId ? status.data.latest_regrade : undefined;
	const setGrade = useCallback((next: string | null) => void setParam(gradeParam(next, latest)), [setParam, latest]);
	return { grade: effectiveGrade(param, latest), latest, setGrade };
}

/** A re-score's note without a closing full stop, so the sentence around it ends with one. */
function trimNote(note: string | undefined): string {
	return (note ?? "").trim().replace(/[.\s]+$/, "");
}

/**
 * One line under the comparison header whenever the comparison has a re-score: which result the page
 * shows (the newest re-score by default, or the original) with a link to the other one. Another
 * re-score or human reviews picked on the Result tab say so there instead.
 */
export function RegradeNotice({ expId }: { expId: string }) {
	const { t, i18n } = useTranslation("hx01");
	const { grade, latest } = useGradeChoice(expId);
	const [tab] = useQueryState("tab", parseAsString);
	if (!latest || (grade !== null && grade !== latest.id)) return null;
	const date = formatDateTime(latest.created_at, i18n.language) ?? latest.created_at;
	const note = trimNote(latest.note);
	const showingRegrade = grade === latest.id;
	return (
		<p
			className="border-chart-info/40 bg-chart-info/5 text-foreground flex flex-wrap items-center gap-x-2 gap-y-1 rounded-lg border px-4 py-2 text-sm"
			role="note"
			data-testid="regrade-notice"
			data-showing={showingRegrade ? "regrade" : "original"}
		>
			<RefreshCw className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
			<span className="min-w-0">
				{showingRegrade
					? note
						? t("regradeNotice.showingNote", { id: latest.id, date, note })
						: t("regradeNotice.showing", { id: latest.id, date })
					: t("regradeNotice.original", { id: latest.id, date })}
			</span>
			<Link
				to="/experiments/$expId"
				params={{ expId }}
				search={{ tab: tab ?? undefined, grade: showingRegrade ? ORIGINAL_GRADE : undefined }}
				className="text-primary font-medium hover:underline"
				data-testid={showingRegrade ? "regrade-notice-original" : "regrade-notice-latest"}
			>
				{showingRegrade ? t("regradeNotice.showOriginal") : t("regradeNotice.showLatest")}
			</Link>
		</p>
	);
}