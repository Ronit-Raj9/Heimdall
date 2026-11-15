import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { useEstimateExperimentMutation } from "@/lib/store/apis/benchApi";
import type { Experiment } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { EstimateBox } from "./estimateBox";

/**
 * Asks POST /estimate for the experiment just planned, with the same body as the plan, and shows
 * the answer. A new experiment object asks again; the previous request is aborted.
 */
export function PlanEstimate({ experiment, className }: { experiment: Experiment; className?: string }) {
	const { t } = useTranslation("bench");
	const [estimate, state] = useEstimateExperimentMutation();

	useEffect(() => {
		const request = estimate(experiment);
		return () => request.abort();
	}, [estimate, experiment]);

	return (
		<section className={cn("flex flex-col gap-2 border-t pt-4", className)} data-testid="estimate-card">
			<div className="flex flex-col gap-0.5">
				<h3 className="text-sm font-semibold">{t("experiments.new.estimate.title")}</h3>
				<p className="text-muted-foreground text-xs">{t("experiments.new.estimate.description")}</p>
			</div>
			{state.isLoading ? (
				<BlockSkeleton rows={4} testId="estimate-loading" />
			) : state.error ? (
				<LoadError
					title={t("experiments.new.estimate.loadError")}
					error={state.error}
					onRetry={() => void estimate(experiment)}
					testId="estimate-error"
				/>
			) : state.data ? (
				<EstimateBox forecast={state.data} />
			) : null}
		</section>
	);
}