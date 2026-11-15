import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { TONE_CLASS, formatCount, formatSignedPp } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { MatrixPair, MatrixRow } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { formatCellP, pairIndex, pairTone } from "./matrix.utils";

/**
 * Arms by arms. Each cell is the Holm-adjusted p of the row arm against the column arm; a
 * significant cell is bold and coloured by direction, and the title carries the reason and the
 * difference. The diagonal is empty.
 */
export function MatrixPairGrid({
	rows,
	pairs,
}: {
	rows: readonly Pick<MatrixRow, "arm" | "label" | "model">[];
	pairs: readonly MatrixPair[];
}) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const lookup = pairIndex(pairs);

	return (
		<Table data-testid="matrix-grid" aria-label={t("matrix.grid.label")} containerClassName="rounded-sm" className="w-auto">
			<TableHeader>
				<TableRow className="hover:bg-transparent">
					<TableHead className="text-muted-foreground text-[11px] font-normal">{t("matrix.grid.rowVsColumn")}</TableHead>
					{rows.map((col) => (
						<TableHead key={col.arm} className="text-center font-mono" title={`${col.label} (${col.model})`} scope="col">
							{col.arm}
						</TableHead>
					))}
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row) => (
					<TableRow key={row.arm} className="hover:bg-transparent">
						<TableHead className="font-mono" title={`${row.label} (${row.model})`} scope="row">
							{row.arm}
						</TableHead>
						{rows.map((col) => {
							const testId = `matrix-cell-${row.arm}-${col.arm}`;
							if (row.arm === col.arm) {
								return (
									<TableCell
										key={col.arm}
										className="bg-muted/40"
										aria-label={t("matrix.grid.same")}
										data-testid={testId}
										data-state="same"
									/>
								);
							}
							const cell = lookup(row.arm, col.arm);
							const pAdjusted = formatCellP(cell?.pair.p_adjusted, locale);
							if (!cell || pAdjusted === null) {
								return (
									<TableCell
										key={col.arm}
										className="text-muted-foreground text-center text-xs italic"
										title={cell?.pair.reason}
										data-testid={testId}
										data-state="none"
									>
										{t("matrix.grid.noTest")}
									</TableCell>
								);
							}
							const tone = pairTone(cell);
							const hint = t("matrix.grid.hint", {
								row: row.arm,
								column: col.arm,
								reason: cell.pair.reason,
								diff: formatSignedPp(cell.diff, locale),
								tasks: formatCount(cell.pair.tasks, locale),
								nonzero: formatCount(cell.pair.n_nonzero, locale),
								p: formatCellP(cell.pair.p, locale) ?? t("matrix.grid.noP"),
								pAdjusted,
							});
							return (
								<TableCell key={col.arm} className="p-1 text-center" data-testid={testId}>
									<span
										title={hint}
										aria-label={hint}
										className={cn(
											"inline-block min-w-14 rounded-sm border px-1.5 py-0.5 text-xs tabular-nums",
											cell.pair.significant ? cn("font-bold", TONE_CLASS[tone]) : "text-muted-foreground border-transparent",
										)}
										data-state={cell.pair.significant ? "significant" : "not-significant"}
										data-tone={tone}
									>
										{pAdjusted}
									</span>
								</TableCell>
							);
						})}
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}