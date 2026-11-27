import { MatrixReport } from "@/components/matrix/matrixReport";
import { LoadError } from "@/components/runs/screenStates";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { useGetVerdictQuery } from "@/lib/store";
import { useGetModelMatrixQuery } from "@/lib/store/apis/benchApi";
import type { ModelMatrix, SetupsVerdict } from "@/lib/types/bench";
import { formatUsd2, observedNumber } from "@/components/verdict/armCost";
import { plainSetupsNote } from "@/components/verdict/setupsTable";
import "@/components/experiments/ufrcI18n";
import { getLocale } from "@/lib/i18n";
import type { MatrixRow } from "@/lib/types/bench";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";
import { SlowSkeleton } from "./slowSkeleton";

/**
 * The Matrix tab: every arm of the experiment ranked, with every pair tested (docs/model-matrix.md).
 * ?condition=, ?grade_source= and ?alpha= carry over from the old /matrix URL.
 */
export function MatrixTab({ expId, gradeSource: pageSource }: { expId: string; gradeSource?: string }) {
	const { t } = useTranslation("bench");
	const { t: tr0 } = useTranslation("ufrc");
	const tr = tr0 as unknown as Translate;
	const [condition, setCondition] = useQueryState("condition", parseAsString);
	const [gradeSource] = useQueryState("grade_source", parseAsString);
	const [alpha] = useQueryState("alpha", parseAsString);
	const matrix = useGetModelMatrixQuery({
		expId,
		condition: condition ?? undefined,
		// A grade source picked on this tab wins; otherwise the one the page shows (decision 1).
		grade_source: gradeSource ?? pageSource,
		alpha: alpha ?? undefined,
	});
	// The default view (no condition, grade source or alpha chosen) is the analysis's own matrix, so its
	// tiers and note come from the verdict's setups result: the Result tab and this tab tell one story (N244).
	const asAnalysed = !condition && !gradeSource && !alpha;
	const verdict = useGetVerdictQuery(pageSource ? { expId, gradeSource: pageSource } : expId, { skip: !asAnalysed });
	const setups = asAnalysed ? verdict.data?.verdict.setups : undefined;

	if (matrix.isLoading) return <SlowSkeleton rows={6} testId="matrix-report-loading" onRetry={() => void matrix.refetch()} />;
	if (matrix.error || !matrix.data) {
		return <LoadError title={t("matrix.loadError")} error={matrix.error} onRetry={() => void matrix.refetch()} testId="matrix-error" />;
	}
	const shown = withSetups(matrix.data, setups, tr);
	const rawNote = setups?.note || matrix.data.note;
	const cheapest = shown.separable ? null : cheapestSetup(shown.rows);
	return (
		<div className="flex flex-col gap-6" data-testid="matrix-screen">
			<div className="flex flex-wrap items-center gap-2">
				<GradeSourceTag source={matrix.data.grade_source} testId="matrix-source" />
			</div>
			{/* Q-253: when the scores cannot be told apart, the cost decides: the cheapest setup comes before any statistics. */}
			{cheapest && (
				<p className="text-foreground text-sm" data-testid="matrix-cheapest">
					{tr("setups.cheapestTie", { setup: cheapest.arm, cost: formatUsd2(cheapest.cost, getLocale()) })}
				</p>
			)}
			<MatrixReport matrix={shown} onCondition={(next) => void setCondition(next)} />
			{rawNote && rawNote !== shown.note && (
				<details className="text-xs" data-testid="matrix-server-note">
					<summary className="text-muted-foreground hover:text-foreground cursor-pointer select-none">{tr("setups.serverNote")}</summary>
					<p className="text-muted-foreground mt-2">{rawNote}</p>
				</details>
			)}
		</div>
	);
}

/**
 * The matrix with the tiers and note of the analysis's setups verdict (fix wave 3, N244): a setup in
 * the top group is tier 1, one behind is tier 2. Without a setups verdict the matrix is unchanged.
 */
export function withSetups(matrix: ModelMatrix, setups: SetupsVerdict | undefined, t?: Translate): ModelMatrix {
	if (!setups) return matrix;
	return {
		...matrix,
		// Q-140: the note in plain words when a translator is given; the raw note goes under the report.
		note: t ? plainSetupsNote(setups, t) : setups.note || matrix.note,
		separable: setups.separable,
		rows: matrix.rows.map((row) =>
			setups.behind.includes(row.arm) ? { ...row, tier: 2 } : setups.top_group.includes(row.arm) ? { ...row, tier: 1 } : row,
		),
	};
}
type Translate = (key: string, opts?: Record<string, unknown>) => string;

/** The setup with the lowest known median cost per run, or null when no cost is known (Q-253). Pure. */
export function cheapestSetup(rows: readonly Pick<MatrixRow, "arm" | "metrics">[]): { arm: string; cost: number } | null {
	let best: { arm: string; cost: number } | null = null;
	for (const row of rows) {
		const cost = observedNumber(row.metrics?.cost_usd);
		if (cost !== null && (best === null || cost < best.cost)) best = { arm: row.arm, cost };
	}
	return best;
}