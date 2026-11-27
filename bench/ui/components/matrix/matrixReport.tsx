import { EmptyNote, Section } from "@/components/runs/screenStates";
import { Card } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { ModelMatrix } from "@/lib/types/bench";
import { AlertTriangle, Info } from "lucide-react";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import { MatrixPairGrid } from "./matrixPairGrid";
import { MatrixTable } from "./matrixTable";
import { Disclosure } from "@/components/verdict/disclosure";
import "@/components/verdict/uf4aI18n";

export interface MatrixReportProps {
	matrix: ModelMatrix;
	/** Called with the condition picked in the switch; the screen puts it in the URL. */
	onCondition: (condition: string) => void;
}

/**
 * One experiment's model matrix: the condition switch and facts, the "cannot separate" statement
 * shown before any number when nothing can be told apart, the arm table, the pairwise grid and the
 * runs left out.
 */
/** True when some pair differs below alpha, so the pairwise table opens by itself (M4-50). Pure. */
export function anyPairBelow(pairs: readonly { p_adjusted?: number | null }[], alpha: number | null | undefined): boolean {
	if (alpha == null) return false;
	return pairs.some((pair) => pair.p_adjusted != null && pair.p_adjusted < alpha);
}

export function MatrixReport({ matrix, onCondition }: MatrixReportProps) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("uf4a");
	const id = useId();
	const rows = matrix.rows ?? [];
	const pairs = matrix.pairs ?? [];
	const conditions = matrix.conditions ?? [];
	const excluded = matrix.excluded ?? [];

	return (
		<div className="flex flex-col gap-6" data-testid="matrix-report" data-separable={matrix.separable ? "true" : "false"}>
			<div className="flex flex-wrap items-end gap-4">
				{/* M4-52: a single fault ("clean") is no choice: the select shows only with two or more. */}
				{conditions.length > 1 && (
					<div className="flex flex-col gap-1">
						<label htmlFor={id} className="text-muted-foreground text-xs">
							{t("matrix.condition")}
						</label>
						<Select value={matrix.condition || undefined} onValueChange={onCondition}>
							<SelectTrigger id={id} className="min-w-40 font-mono text-xs" data-testid="matrix-condition">
								<SelectValue />
							</SelectTrigger>
							<SelectContent data-testid="matrix-condition-content">
								{conditions.map((condition) => (
									<SelectItem
										key={condition}
										value={condition}
										className="font-mono text-xs"
										data-testid={`matrix-condition-option-${condition}`}
									>
										{condition}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
				)}
				<p className="text-muted-foreground pb-1.5 text-xs" data-testid="matrix-facts">
					{matrix.grade_source
						? tu("matrix.factsGrades", { count: matrix.tasks, grades: matrix.grade_source })
						: tu("matrix.facts", { count: matrix.tasks })}
				</p>
			</div>

			{!matrix.separable ? (
				<div
					role="status"
					className="border-warning bg-warning-soft flex items-start gap-3 rounded-sm border px-4 py-3"
					data-testid="matrix-not-separable"
				>
					<AlertTriangle className="text-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					<div className="flex flex-col gap-0.5">
						<p className="text-warning-ink text-sm font-semibold">{t("matrix.notSeparable")}</p>
						<p className="text-foreground text-sm" data-testid="matrix-plain-note">
							{tu("matrix.tooFew", { count: matrix.tasks })}
						</p>
					</div>
				</div>
			) : (
				matrix.note && (
					<p className="text-muted-foreground flex items-start gap-2 text-xs" data-testid="matrix-note">
						<Info className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
						{matrix.note}
					</p>
				)
			)}

			{matrix.synthetic && (
				<p role="note" className="text-warning-ink flex items-center gap-1.5 text-xs font-medium" data-testid="matrix-synthetic">
					<AlertTriangle className="size-3.5" aria-hidden="true" />
					{t("matrix.synthetic")}
				</p>
			)}

			<Section title={t("matrix.table.title")} description={t("matrix.table.description")} testId="matrix-table-section">
				{rows.length === 0 ? (
					<EmptyNote testId="matrix-empty">{t("matrix.empty")}</EmptyNote>
				) : (
					<Card className="py-0 shadow-none">
						<MatrixTable rows={rows} />
					</Card>
				)}
			</Section>

			{/* M4-50, M4-46: the pairwise luck chances and the server's own note are for statisticians; open only when a pair differs. */}
			{(rows.length > 1 || (!matrix.separable && matrix.note)) && (
				<Disclosure
					title={tu("verdict.forStatisticians")}
					defaultOpen={anyPairBelow(pairs, matrix.alpha)}
					testId="matrix-statistics"
				>
					<div className="flex flex-col gap-4">
						{!matrix.separable && matrix.note && (
							<p className="text-muted-foreground text-xs" data-testid="matrix-stat-note">
								{matrix.note}
							</p>
						)}
						{rows.length > 1 && (
							<Section
								as="h4"
								title={t("matrix.grid.title")}
								description={t("matrix.grid.description", { alpha: matrix.alpha })}
								testId="matrix-grid-section"
							>
								<div className="w-auto max-w-full overflow-x-auto">
									<MatrixPairGrid rows={rows} pairs={pairs} />
								</div>
							</Section>
						)}
					</div>
				</Disclosure>
			)}

			{excluded.length > 0 && (
				<ul className="text-muted-foreground flex flex-col gap-0.5 text-xs" data-testid="matrix-excluded">
					{excluded.map((x) => (
						<li key={x.reason}>{t("matrix.excluded", { reason: x.reason, runs: x.runs })}</li>
					))}
				</ul>
			)}
		</div>
	);
}