import { MatrixReport } from "@/components/matrix/matrixReport";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { useGetVerdictQuery } from "@/lib/store";
import { useGetModelMatrixQuery } from "@/lib/store/apis/benchApi";
import type { ModelMatrix, SetupsVerdict } from "@/lib/types/bench";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

/**
 * The Matrix tab: every arm of the experiment ranked, with every pair tested (docs/model-matrix.md).
 * ?condition=, ?grade_source= and ?alpha= carry over from the old /matrix URL.
 */
export function MatrixTab({ expId, gradeSource: pageSource }: { expId: string; gradeSource?: string }) {
	const { t } = useTranslation("bench");
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

	if (matrix.isLoading) return <BlockSkeleton rows={6} testId="matrix-report-loading" />;
	if (matrix.error || !matrix.data) {
		return <LoadError title={t("matrix.loadError")} error={matrix.error} onRetry={() => void matrix.refetch()} testId="matrix-error" />;
	}
	return (
		<div className="flex flex-col gap-6" data-testid="matrix-screen">
			<div className="flex flex-wrap items-center gap-2">
				<GradeSourceTag source={matrix.data.grade_source} testId="matrix-source" />
			</div>
			<MatrixReport matrix={withSetups(matrix.data, setups)} onCondition={(next) => void setCondition(next)} />
		</div>
	);
}

/**
 * The matrix with the tiers and note of the analysis's setups verdict (fix wave 3, N244): a setup in
 * the top group is tier 1, one behind is tier 2. Without a setups verdict the matrix is unchanged.
 */
export function withSetups(matrix: ModelMatrix, setups: SetupsVerdict | undefined): ModelMatrix {
	if (!setups) return matrix;
	return {
		...matrix,
		note: setups.note || matrix.note,
		separable: setups.separable,
		rows: matrix.rows.map((row) =>
			setups.behind.includes(row.arm) ? { ...row, tier: 2 } : setups.top_group.includes(row.arm) ? { ...row, tier: 1 } : row,
		),
	};
}