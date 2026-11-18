import { useExperimentId } from "@/components/experiments/useExperimentId";
import { MatrixReport } from "@/components/matrix/matrixReport";
import { CostExplainPanel } from "@/components/migration/costWaterfall"; // W6 cost explanation
import { ExperimentParetoPanel } from "@/components/migration/migrationReport"; // W6 cost against verified success
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { useGetModelMatrixQuery } from "@/lib/store/apis/benchApi";
import { Link } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

/**
 * Model matrix: /matrix?exp=&condition=&grade_source=&alpha=. Every arm of one experiment ranked,
 * with every pair tested (docs/model-matrix.md). Without exp it shows the newest experiment.
 */
export default function MatrixView() {
	const { t } = useTranslation("bench");
	const [expParam] = useQueryState("exp", parseAsString);
	const experiment = useExperimentId(expParam);

	return (
		<div className="flex flex-col gap-6" data-testid="matrix-screen">
			<header className="flex flex-col gap-1">
				<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("matrix.title")}</h1>
				<p className="text-muted-foreground max-w-prose text-sm">{t("matrix.subtitle")}</p>
			</header>
			{experiment.isLoading ? (
				<BlockSkeleton rows={6} testId="matrix-loading" />
			) : experiment.error ? (
				<LoadError title={t("matrix.listError")} error={experiment.error} onRetry={experiment.refetch} testId="matrix-list-error" />
			) : !experiment.expId ? (
				<EmptyNote testId="matrix-no-experiments">{t("matrix.noExperiments")}</EmptyNote>
			) : (
				<ExperimentMatrix expId={experiment.expId} />
			)}
		</div>
	);
}

function ExperimentMatrix({ expId }: { expId: string }) {
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
		<div className="flex flex-col gap-6">
			<div className="flex flex-wrap items-center gap-3">
				<span className="text-muted-foreground font-mono text-xs">{t("matrix.experiment", { id: matrix.data.exp_id || expId })}</span>
				<ModeBadge mode={matrix.data.mode} />
				<Link
					to="/verdict"
					search={{ exp: expId }}
					className="text-primary text-xs underline-offset-4 hover:underline"
					data-testid="matrix-open-verdict"
				>
					{t("matrix.openVerdict")}
				</Link>
			</div>
			<MatrixReport matrix={matrix.data} onCondition={(next) => void setCondition(next)} />
			{/* W6 cost against verified success and why arm B cost more than arm A */}
			<ExperimentParetoPanel expId={expId} condition={condition ?? undefined} />
			<CostExplainPanel expId={expId} condition={condition ?? undefined} />
		</div>
	);
}