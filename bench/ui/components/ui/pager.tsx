import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Rows per page of every client-sliced list (Spec 10). */
export const PAGE_SIZE = 50;

/** The page window of a list: clamps the page and gives the 1-based first and last row shown. */
export function pageWindow(total: number, page: number, size = PAGE_SIZE): { page: number; pageCount: number; first: number; last: number; start: number; end: number } {
	const pageCount = Math.max(1, Math.ceil(total / size));
	const p = Math.min(Math.max(1, Math.floor(page) || 1), pageCount);
	const start = (p - 1) * size;
	const end = Math.min(total, start + size);
	return { page: p, pageCount, first: total === 0 ? 0 : start + 1, last: end, start, end };
}

/** One page of rows: pageSlice(rows, page).rows; the page is clamped to the last page. */
export function pageSlice<T>(rows: readonly T[], page: number, size = PAGE_SIZE): { rows: T[]; page: number; pageCount: number; first: number; last: number } {
	const w = pageWindow(rows.length, page, size);
	return { rows: rows.slice(w.start, w.end), page: w.page, pageCount: w.pageCount, first: w.first, last: w.last };
}

/** The page that holds row `index` (0-based), for a deep link such as ?task=. */
export function pageOfIndex(index: number, size = PAGE_SIZE): number {
	return index < 0 ? 1 : Math.floor(index / size) + 1;
}

export interface PagerProps {
	/** 1-based. */
	page: number;
	pageCount: number;
	onPage(page: number): void;
	/** The range text of the caller's list, for example t("suites.tasks.pageOf", { first, last, total }); "Page 3 of 73" when absent. */
	rangeText?: string;
	testId: string;
	className?: string;
}

/** Previous, "Page 3 of 73", Next. Hidden buttons only when there is one page. Keep ?page= in the URL. */
export function Pager({ page, pageCount, onPage, rangeText, testId, className }: PagerProps) {
	const { t } = useTranslation("ui");
	return (
		<nav className={cn("flex flex-wrap items-center justify-between gap-2 text-xs", className)} aria-label={t("pager.label")} data-testid={testId}>
			<span className="text-muted-foreground tabular-nums" aria-live="polite">
				{rangeText ?? t("pager.page", { page, pageCount })}
			</span>
			{pageCount > 1 && (
				<span className="flex items-center gap-1">
					<Button variant="outline" size="sm" disabled={page <= 1} onClick={() => onPage(page - 1)} dataTestId={`${testId}-prev`}>
						<ChevronLeft aria-hidden="true" />
						{t("pager.prev")}
					</Button>
					{rangeText && (
						<span className="text-muted-foreground px-1 tabular-nums">{t("pager.page", { page, pageCount })}</span>
					)}
					<Button variant="outline" size="sm" disabled={page >= pageCount} onClick={() => onPage(page + 1)} dataTestId={`${testId}-next`}>
						{t("pager.next")}
						<ChevronRight aria-hidden="true" />
					</Button>
				</span>
			)}
		</nav>
	);
}

type CountT = (key: string, opts?: Record<string, unknown>) => string;

/**
 * The list count text. "12 of 80", plus "(9 hidden)" when test items are hidden; when every item
 * is shown and only test items are hidden it reads "59 benchmarks, 21 test items hidden" instead
 * of "59 of 59 (21 hidden)" (Q-56). Pure.
 */
export function listCountText(t: CountT, shown: number, total: number, hidden?: number, noun?: string): string {
	const fmt = (n: number) => n.toLocaleString("en-US");
	if (hidden && shown === total) {
		return noun
			? t("listCount.allShownHidden", { ns: "platform", total: fmt(total), noun, count: hidden })
			: t("listCount.allShownHiddenNoNoun", { ns: "platform", total: fmt(total), count: hidden });
	}
	const base = t("list.count", { ns: "ui", shown: fmt(shown), total: fmt(total) });
	return hidden ? `${base} ${t("list.hidden", { ns: "ui", count: hidden })}` : base;
}

/** The list count at the right of a toolbar (see listCountText). noun names the items, plural: "benchmarks". */
export function ListCount({
	shown,
	total,
	hidden,
	noun,
	testId,
	className,
}: {
	shown: number;
	total: number;
	hidden?: number;
	noun?: string;
	testId: string;
	className?: string;
}) {
	const { t } = useTranslation("ui");
	return (
		<span className={cn("text-muted-foreground text-xs whitespace-nowrap tabular-nums", className)} data-testid={testId}>
			{listCountText(t as unknown as CountT, shown, total, hidden, noun)}
		</span>
	);
}

/**
 * Column priority on phones (Spec 10, M1-57). Give it to TableHead and TableCell (as className and
 * data-priority): below sm, secondary columns hide; primary, status and metric stay, so the status
 * or result column is always on screen.
 */
export type ColumnPriority = "primary" | "status" | "metric" | "secondary";

export function priorityClass(priority: ColumnPriority | undefined): string {
	return priority === "secondary" ? "max-sm:hidden" : "";
}

export function priorityProps(priority: ColumnPriority | undefined): { "data-priority"?: ColumnPriority; className: string } {
	return priority ? { "data-priority": priority, className: priorityClass(priority) } : { className: "" };
}
