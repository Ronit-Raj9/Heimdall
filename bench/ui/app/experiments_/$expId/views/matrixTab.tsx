import { MatrixReport } from "@/components/matrix/matrixReport";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { useGetModelMatrixQuery } from "@/lib/store/apis/benchApi";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

/**
 * The Matrix tab: every arm of the experiment ranked, with every pair tested (docs/model-matrix.md).
 * ?condition=, ?grade_source= and ?alpha= carry over from the old /matrix URL.
 */
export function MatrixTab({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	const [condition, setCondition] = useQueryState("condition", parseAsString);
	const [gradeSource] = useQueryState("grade_source", parseAsString);
	const [alpha] = useQueryState("alpha", parseAsString);
	const matrix = useGetModelMatrixQuery({
		expId,
		condition: condition ?? undefined,
		grade_source: gradeSource ?? undefined,
		alpha: alpha ?? undefined,
	});

	if (matrix.isLoading) return <BlockSkeleton rows={6} testId="matrix-report-loading" />;
	if (matrix.error || !matrix.data) {
		return <LoadError title={t("matrix.loadError")} error={matrix.error} onRetry={() => void matrix.refetch()} testId="matrix-error" />;
	}
	return (
		<div className="flex flex-col gap-6" data-testid="matrix-screen">
			<div className="flex flex-wrap items-center gap-2">
				<ModeBadge mode={matrix.data.mode} />
				<GradeSourceTag source={matrix.data.grade_source} testId="matrix-source" />
			</div>
			<MatrixReport matrix={matrix.data} onCondition={(next) => void setCondition(next)} />
		</div>
	);
}