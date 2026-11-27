import * as React from "react";

import { priorityProps, type ColumnPriority } from "@/components/ui/pager";
import { useScrollEdges } from "@/lib/hooks/useScrollEdges";
import { cn } from "@/lib/utils";

interface TableProps extends React.ComponentProps<"table"> {
	containerClassName?: string;
	/**
	 * Leading columns that stay in place while the table scrolls sideways, so a row always shows
	 * whose numbers it holds. Offsets are measured from the header row.
	 */
	stickyColumns?: number;
	/** A long table scrolls inside itself (at most 70% of the viewport) and keeps its header row in view. */
	scrollY?: boolean;
	/**
	 * Below sm each row stacks into key and value lines (C2-8, M2-37, M3-8): the header row is kept
	 * for screen readers only and a cell shows its `label` before its value. Off by default.
	 */
	stack?: boolean;
	/**
	 * false: no fixed max height on the scrolling box (from stickyColumns or scrollY), so a short
	 * table, for example rows opened from a fold, shows whole. On by default.
	 */
	capHeight?: boolean;
}

/** The classes of a stacked table below sm (see TableProps.stack). Pure, exported for tests. */
export const STACK_CLASSES =
	"max-sm:[&_thead]:sr-only max-sm:[&_tbody_tr]:flex max-sm:[&_tbody_tr]:h-auto max-sm:[&_tbody_tr]:flex-col max-sm:[&_tbody_tr]:gap-1 max-sm:[&_tbody_tr]:py-2 max-sm:[&_tbody_td]:flex max-sm:[&_tbody_td]:justify-between max-sm:[&_tbody_td]:gap-4 max-sm:[&_tbody_td]:px-4 max-sm:[&_tbody_td]:whitespace-normal max-sm:[&_tbody_th]:px-4 max-sm:[&_tbody_th]:text-left max-sm:[&_td[data-label]]:before:text-muted-foreground max-sm:[&_td[data-label]]:before:content-[attr(data-label)]";

/** Marks the first `count` columns of every row as sticky and sets their left offsets. */
function pinLeadingColumns(table: HTMLTableElement, count: number) {
	const headRow = table.tHead?.rows[0] ?? table.rows[0];
	if (!headRow) return;
	const offsets: number[] = [];
	let x = 0;
	let col = 0;
	for (const cell of Array.from(headRow.cells)) {
		if (col >= count) break;
		for (let i = 0; i < cell.colSpan && col < count; i++, col++) offsets.push(x);
		x += cell.getBoundingClientRect().width;
	}
	// Header rows under a rowSpan start mid-table, so only the first header row is pinned.
	const rows = [headRow, ...Array.from(table.tBodies).flatMap((body) => Array.from(body.rows))];
	for (const row of rows) {
		let index = 0;
		for (const cell of Array.from(row.cells)) {
			const end = index + cell.colSpan;
			const pinned = end <= count;
			if (pinned) {
				cell.setAttribute("data-sticky", "");
				cell.style.left = `${offsets[index] ?? 0}px`;
				if (end === count) cell.setAttribute("data-sticky-edge", "");
				else cell.removeAttribute("data-sticky-edge");
			} else if (cell.hasAttribute("data-sticky")) {
				cell.removeAttribute("data-sticky");
				cell.removeAttribute("data-sticky-edge");
				cell.style.left = "";
			}
			index = end;
			if (index >= count && !pinned) break;
		}
	}
}

function Table({ className, containerClassName, stickyColumns = 0, scrollY = false, stack = false, capHeight = true, ...props }: TableProps) {
	const containerRef = React.useRef<HTMLDivElement>(null);
	// Fade the edge that hides columns (X-95); pinned leading columns never fade.
	useScrollEdges(containerRef, { start: stickyColumns <= 0 });
	const tableRef = React.useRef<HTMLTableElement>(null);
	const [isScrollable, setIsScrollable] = React.useState(false);

	// A horizontally scrolling region must be reachable by keyboard (WCAG 2.1.1), but only
	// add the tab stop when the table actually overflows, so short tables stay out of tab order.
	React.useEffect(() => {
		const container = containerRef.current;
		if (!container) return;
		const update = () => setIsScrollable(container.scrollWidth > container.clientWidth);
		update();
		const observer = new ResizeObserver(update);
		observer.observe(container);
		if (container.firstElementChild) observer.observe(container.firstElementChild);
		return () => observer.disconnect();
	}, []);

	// The edge shadow after the pinned columns shows only once the table has moved sideways.
	React.useEffect(() => {
		const container = containerRef.current;
		if (!container || stickyColumns <= 0) return;
		const onScroll = () => container.toggleAttribute("data-scrolled", container.scrollLeft > 1);
		onScroll();
		container.addEventListener("scroll", onScroll, { passive: true });
		return () => container.removeEventListener("scroll", onScroll);
	}, [stickyColumns]);

	// Re-measure after every render (rows change with sorting and filters) and on resize.
	React.useLayoutEffect(() => {
		const table = tableRef.current;
		if (!table || stickyColumns <= 0) return;
		pinLeadingColumns(table, stickyColumns);
	});
	React.useEffect(() => {
		const table = tableRef.current;
		if (!table || stickyColumns <= 0 || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(() => pinLeadingColumns(table, stickyColumns));
		observer.observe(table);
		return () => observer.disconnect();
	}, [stickyColumns]);

	return (
		<div
			ref={containerRef}
			data-slot="table-container"
			className={cn(
				"relative w-full overflow-x-auto overscroll-x-contain",
				// A pinned table also caps its height near the viewport, so a long one scrolls inside
				// itself and keeps its header row in view.
				capHeight && stickyColumns > 0 && "max-h-[max(24rem,calc(100dvh-10rem))] overflow-y-auto",
				capHeight && scrollY && "max-h-[70vh] overflow-y-auto",
				containerClassName,
			)}
			tabIndex={isScrollable ? 0 : undefined}
		>
			<table
				ref={tableRef}
				data-slot="table"
				data-stack={stack ? "" : undefined}
				className={cn("w-full caption-bottom text-sm", stack && STACK_CLASSES, className)}
				{...props}
			/>
		</div>
	);
}

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
	return <thead data-slot="table-header" className={cn("[&_tr]:border-b", className)} {...props} />;
}

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
	return <tbody data-slot="table-body" className={cn("[&_tr:last-child]:border-0", className)} {...props} />;
}

function TableFooter({ className, ...props }: React.ComponentProps<"tfoot">) {
	return <tfoot data-slot="table-footer" className={cn("bg-muted/50 border-t font-medium [&>tr]:last:border-b-0", className)} {...props} />;
}

/**
 * Column priority on phones (M1-57, Spec 10): "secondary" cells and heads are hidden below sm, so a
 * row keeps its name, status and one number on a 390 px screen. Give the head and the cells of a
 * column the same value.
 */
export type { ColumnPriority };

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
	return (
		<tr
			data-slot="table-row"
			className={cn("hover:bg-(--table-hover-bg) data-[state=selected]:bg-muted h-(--row-h) border-b transition-colors duration-100", className)}
			{...props}
		/>
	);
}

function TableHead({
	className,
	priority,
	numeric,
	scope = "col",
	...props
}: React.ComponentProps<"th"> & { priority?: ColumnPriority; /** Right aligned, for a column of numbers or money. */ numeric?: boolean }) {
	return (
		<th
			data-slot="table-head"
			scope={scope}
			data-priority={priorityProps(priority)["data-priority"]}
			className={cn(
				priorityProps(priority).className,
				"text-muted-foreground sticky top-0 z-[2] h-(--row-h) bg-(--table-head-bg) px-4 text-left align-middle text-xs font-medium tracking-[0.01em] whitespace-nowrap [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]",
				numeric && "text-right",
				className,
			)}
			{...props}
		/>
	);
}

function TableCell({
	className,
	priority,
	label,
	numeric,
	...props
}: React.ComponentProps<"td"> & {
	priority?: ColumnPriority;
	/** The column name a stacked table shows before the value. */
	label?: string;
	/** Right aligned with tabular numerals, for numbers and money. */
	numeric?: boolean;
}) {
	return (
		<td
			data-slot="table-cell"
			data-label={label}
			data-priority={priorityProps(priority)["data-priority"]}
			className={cn(
				priorityProps(priority).className,
				"px-4 py-1 align-middle whitespace-nowrap tabular-nums [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]",
				numeric && "text-right",
				className,
			)}
			{...props}
		/>
	);
}

function TableCaption({ className, ...props }: React.ComponentProps<"caption">) {
	return <caption data-slot="table-caption" className={cn("text-muted-foreground mt-4 text-sm", className)} {...props} />;
}

/**
 * A name cell with a small second line (round 3): tight leading so the row is about 48 px, not 53.
 * The second line is helper text (benchmark, id); it truncates instead of wrapping.
 */
function TableTwoLine({ primary, secondary, className }: { primary: React.ReactNode; secondary?: React.ReactNode; className?: string }) {
	return (
		<div data-slot="table-two-line" className={cn("flex min-w-0 flex-col justify-center", className)}>
			<span className="truncate text-sm leading-5">{primary}</span>
			{secondary !== undefined && secondary !== null && secondary !== "" && (
				<span className="text-muted-foreground truncate text-xs leading-4">{secondary}</span>
			)}
		</div>
	);
}

/** The standard last column for the row menu: 56 px wide, right aligned, no header text. */
function TableMenuHead({ className, label, ...props }: React.ComponentProps<"th"> & { /** Screen reader name, for example "Actions". */ label?: string }) {
	return (
		<th data-slot="table-head" scope="col" className={cn("sticky top-0 z-[2] h-(--row-h) w-14 bg-(--table-head-bg) pr-3 pl-0", className)} {...props}>
			{label && <span className="sr-only">{label}</span>}
		</th>
	);
}
function TableMenuCell({ className, ...props }: React.ComponentProps<"td">) {
	return <td data-slot="table-cell" data-menu-cell="" className={cn("w-14 py-1 pr-3 pl-0 text-right align-middle whitespace-nowrap", className)} {...props} />;
}

/**
 * Whether a column has a value in at least one row, so a page hides a column that is empty in every
 * row (rule 5). Empty means undefined, null, "" or an empty array; 0 and false are values. Pure.
 */
export function columnHasData<T>(rows: readonly T[], pick: (row: T) => unknown): boolean {
	return rows.some((row) => {
		const v = pick(row);
		return !(v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0));
	});
}

export { Table, TableMenuCell, TableMenuHead, TableTwoLine, TableBody, TableCaption, TableCell, TableFooter, TableHead, TableHeader, TableRow };