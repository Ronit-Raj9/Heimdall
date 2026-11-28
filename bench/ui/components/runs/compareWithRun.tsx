import "@/components/runs/a2a/ufl4I18n";
import { StepsSideBySide } from "@/components/compare/stepsSideBySide";
import { DivergenceView } from "@/components/runs/divergencePanel";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { NativeSelect } from "@/components/ui/select";
import { useListRunsQuery } from "@/lib/store/apis/benchApi";
import { useGetRunDivergenceQuery } from "@/lib/store/apis/runDivergenceApi";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** "Compare with another run": pick another run of the same task, see where the two first diverge. */
export function CompareWithRun({ runId, task }: { runId: string; task: string }) {
	const { t } = useTranslation("ufl4");
	const { t: t2 } = useTranslation("ufk2");
	const [other, setOther] = useState("");
	const runs = useListRunsQuery({ task, limit: 50 });
	const div = useGetRunDivergenceQuery(other ? { runId, other } : skipToken);
	if (runs.isLoading) return <BlockSkeleton rows={1} testId="compare-run-loading" />;
	if (runs.error)
		return (
			<LoadError title={t("compare.listError")} error={runs.error} onRetry={() => void runs.refetch()} testId="compare-run-list-error" />
		);
	const options = (runs.data?.runs ?? []).filter((r) => r.run_id !== runId);
	if (options.length === 0) return <p className="text-muted-foreground text-sm">{t("compare.none")}</p>;
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="compare-run">
			<div className="w-full max-w-md">
				<NativeSelect
					value={other}
					onChange={(e) => setOther(e.target.value)}
					aria-label={t("compare.pick")}
					data-testid="compare-run-pick"
				>
					<option value="">{t("compare.pick")}</option>
					{options.map((r) => (
						<option key={r.run_id} value={r.run_id}>
							{r.run_id} · {r.arm} · {r.outcome}
						</option>
					))}
				</NativeSelect>
			</div>
			{other && div.isFetching ? <BlockSkeleton rows={3} testId="compare-run-div-loading" /> : null}
			{other && div.error ? (
				<LoadError title={t("compare.error")} error={div.error} onRetry={() => void div.refetch()} testId="compare-run-div-error" />
			) : null}
			{other && div.data && !div.isFetching ? <DivergenceView data={div.data} /> : null}
			{other && div.data && !div.isFetching ? (
				<StepsSideBySide a={runId} b={other} labelA={t2("steps.thisRun")} labelB={t2("steps.otherRun")} />
			) : null}
		</div>
	);
}