import { EmptyNote, Section } from "@/components/runs/screenStates";
import { Card } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { ModelMatrix } from "@/lib/types/bench";
import { AlertTriangle, Info } from "lucide-react";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import { MatrixPairGrid } from "./matrixPairGrid";
import { MatrixTable } from "./matrixTable";

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
export function MatrixReport({ matrix, onCondition }: MatrixReportProps) {
	const { t } = useTranslation("bench");
	const id = useId();
	const rows = matrix.rows ?? [];
	const pairs = matrix.pairs ?? [];
	const conditions = matrix.conditions ?? [];
	const excluded = matrix.excluded ?? [];

	return (
		<div className="flex flex-col gap-6" data-testid="matrix-report" data-separable={matrix.separable ? "true" : "false"}>
			<div className="flex flex-wrap items-end gap-4">
				{conditions.length > 0 && (
					<div className="flex flex-col gap-1">
						<label htmlFor={id} className="text-muted-foreground text-xs">
							{t("matrix.condition")}
						</label>
						<Select value={matrix.condition || undefined} onValueChange={onCondition} disabled={conditions.length < 2}>
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
				<div className="text-muted-foreground flex flex-col gap-0.5 pb-1.5 text-xs">
					<span data-testid="matrix-facts">{t("matrix.facts", { suite: matrix.suite, tasks: matrix.tasks, alpha: matrix.alpha })}</span>
					{matrix.grade_source && (
						<span className="font-mono" data-testid="matrix-grade-source">
							{t("matrix.gradeSource", { source: matrix.grade_source })}
						</span>
					)}
				</div>
			</div>

			{!matrix.separable ? (
				<div
					role="status"
					className="border-chart-warning/60 bg-chart-warning/10 flex items-start gap-3 rounded-sm border px-4 py-3"
					data-testid="matrix-not-separable"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					<div className="flex flex-col gap-0.5">
						<p className="text-chart-warning-ink text-sm font-semibold">{t("matrix.notSeparable")}</p>
						<p className="text-foreground text-sm">{matrix.note}</p>
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
				<p role="note" className="text-chart-warning-ink flex items-center gap-1.5 text-xs font-medium" data-testid="matrix-synthetic">
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

			{rows.length > 1 && (
				<Section
					title={t("matrix.grid.title")}
					description={t("matrix.grid.description", { alpha: matrix.alpha })}
					testId="matrix-grid-section"
				>
					<Card className="py-0 shadow-none">
						<MatrixPairGrid rows={rows} pairs={pairs} />
					</Card>
				</Section>
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