import { TableHead } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TableSortDir } from "./useTableSort";

export interface SortableHeadProps {
	label: string;
	/** This column is the one the table is sorted by. */
	active: boolean;
	dir: TableSortDir;
	onSort: () => void;
	align?: "left" | "right";
	/** Tooltip on the header button. */
	hint?: string;
	className?: string;
	testId?: string;
}

/**
 * A table header that sorts its column: a real button inside the th, so it is reachable and
 * operable by keyboard, with aria-sort on the th and an arrow for the direction.
 */
export function SortableHead({ label, active, dir, onSort, align = "left", hint, className, testId }: SortableHeadProps) {
	const { t } = useTranslation("bench");
	const Icon = !active ? ArrowUpDown : dir === "asc" ? ArrowUp : ArrowDown;
	const name = t("leaderboard.table.sortBy", { column: label });
	return (
		<TableHead
			aria-sort={active ? (dir === "asc" ? "ascending" : "descending") : "none"}
			className={cn(align === "right" && "text-right", className)}
			data-testid={testId}
		>
			<button
				type="button"
				onClick={onSort}
				title={hint}
				aria-label={active ? `${name}, ${t(dir === "asc" ? "leaderboard.table.sortedAsc" : "leaderboard.table.sortedDesc")}` : name}
				className={cn(
					"focus-visible:ring-ring/50 -mx-1 inline-flex cursor-pointer items-center gap-1 rounded-sm px-1 py-0.5 font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
					active ? "text-foreground" : "text-muted-foreground hover:text-foreground",
					align === "right" && "flex-row-reverse",
				)}
				data-testid={testId ? `${testId}-sort` : undefined}
			>
				<span>{label}</span>
				<Icon className={cn("size-3.5 shrink-0", !active && "opacity-50")} aria-hidden="true" />
			</button>
		</TableHead>
	);
}