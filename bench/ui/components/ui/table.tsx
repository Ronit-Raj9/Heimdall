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
}

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

function Table({ className, containerClassName, stickyColumns = 0, scrollY = false, ...props }: TableProps) {
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
				stickyColumns > 0 && "max-h-[max(24rem,calc(100dvh-10rem))] overflow-y-auto",
				scrollY && "max-h-[70vh] overflow-y-auto",
				containerClassName,
			)}
			tabIndex={isScrollable ? 0 : undefined}
		>
			<table ref={tableRef} data-slot="table" className={cn("w-full caption-bottom text-sm", className)} {...props} />
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

function TableHead({ className, priority, scope = "col", ...props }: React.ComponentProps<"th"> & { priority?: ColumnPriority }) {
	return (
		<th
			data-slot="table-head"
			scope={scope}
			data-priority={priorityProps(priority)["data-priority"]}
			className={cn(
				priorityProps(priority).className,
				"text-muted-foreground sticky top-0 z-[2] h-(--row-h) bg-(--table-head-bg) px-4 text-left align-middle text-xs font-medium tracking-[0.01em] whitespace-nowrap [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]",
				className,
			)}
			{...props}
		/>
	);
}

function TableCell({ className, priority, ...props }: React.ComponentProps<"td"> & { priority?: ColumnPriority }) {
	return (
		<td
			data-slot="table-cell"
			data-priority={priorityProps(priority)["data-priority"]}
			className={cn(
				priorityProps(priority).className,
				"px-4 py-1 align-middle whitespace-nowrap tabular-nums [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]",
				className,
			)}
			{...props}
		/>
	);
}

function TableCaption({ className, ...props }: React.ComponentProps<"caption">) {
	return <caption data-slot="table-caption" className={cn("text-muted-foreground mt-4 text-sm", className)} {...props} />;
}

export { Table, TableBody, TableCaption, TableCell, TableFooter, TableHead, TableHeader, TableRow };