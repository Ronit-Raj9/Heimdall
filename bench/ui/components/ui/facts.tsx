import { cn } from "@/lib/utils";
import { ChevronRight } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";
import type { KeyValueItem } from "./keyValueList";
import "./ufv0I18n";

/**
 * Round 3 pieces that fold noise (rules 7 and 9): a facts list that never stretches across a wide
 * page, and a fold for many rows that all say the same empty thing ("unknown", "not measured").
 */

/** Splits items into the ones to show and the ones to fold behind one line. Pure. */
export function foldEmpty<T>(items: readonly T[], isEmpty: (item: T) => boolean): { shown: T[]; folded: T[] } {
	const shown: T[] = [];
	const folded: T[] = [];
	for (const item of items) (isEmpty(item) ? folded : shown).push(item);
	return { shown, folded };
}

/**
 * One closed line that opens to show folded rows. The default text is "N more not recorded for
 * this run"; pass `summary` for another wording. Renders nothing for count 0.
 */
export function FoldedRows({
	count,
	summary,
	children,
	className,
	testId,
}: {
	count: number;
	summary?: React.ReactNode;
	children: React.ReactNode;
	className?: string;
	testId?: string;
}) {
	const { t } = useTranslation("ufv0");
	if (count <= 0) return null;
	return (
		<details data-slot="folded-rows" data-testid={testId} className={cn("group/fold min-w-0", className)}>
			<summary className="text-muted-foreground hover:text-foreground focus-visible:ring-(--focus) inline-flex min-h-8 cursor-pointer list-none items-center gap-1 rounded-md text-sm transition-colors select-none focus-visible:ring-2 focus-visible:outline-none [&::-webkit-details-marker]:hidden">
				<ChevronRight className="size-4 transition-transform duration-150 group-open/fold:rotate-90" aria-hidden="true" />
				<span>{summary ?? t("fold.notRecorded", { count })}</span>
			</summary>
			<div className="pt-1">{children}</div>
		</details>
	);
}

/**
 * Label and value pairs as a two-column list capped at 640 px (rule 7), so a label is never far
 * from its value on a wide screen. Items for which `isEmpty` is true fold behind one FoldedRows
 * line under the list. `columns={2}` puts pairs side by side from lg (cap 1280 px).
 */
export function FactsList({
	items,
	isEmpty,
	foldSummary,
	columns = 1,
	className,
	testId,
}: {
	items: readonly KeyValueItem[];
	/** Rows to fold, for example (item) => item.value === "unknown". */
	isEmpty?: (item: KeyValueItem) => boolean;
	foldSummary?: React.ReactNode;
	columns?: 1 | 2;
	className?: string;
	testId?: string;
}) {
	const { shown, folded } = isEmpty ? foldEmpty(items, isEmpty) : { shown: [...items], folded: [] as KeyValueItem[] };
	const list = (rows: KeyValueItem[], id?: string) => (
		<dl
			data-slot="facts-list"
			data-testid={id}
			className={cn(
				"grid grid-cols-[minmax(7rem,max-content)_minmax(0,1fr)] gap-x-6 text-sm",
				columns === 2 ? "max-w-[80rem] lg:grid-cols-[minmax(7rem,max-content)_minmax(0,1fr)_minmax(7rem,max-content)_minmax(0,1fr)]" : "max-w-[40rem]",
			)}
		>
			{rows.map((item, i) => (
				<React.Fragment key={item.key ?? i}>
					<dt className="text-muted-foreground border-border/60 border-b py-2">{item.label}</dt>
					<dd className={cn("text-foreground border-border/60 min-w-0 border-b py-2 break-words", item.mono && "font-mono text-xs")}>{item.value}</dd>
				</React.Fragment>
			))}
		</dl>
	);
	return (
		<div className={cn("flex min-w-0 flex-col gap-2", className)} data-testid={testId}>
			{shown.length > 0 && list(shown)}
			<FoldedRows count={folded.length} summary={foldSummary} testId={testId ? `${testId}-folded` : undefined}>
				{list(folded)}
			</FoldedRows>
		</div>
	);
}
