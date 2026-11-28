import { InlineError } from "@/components/runs/screenStates";
import "@/components/suites/ufl5I18n";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/select";
import { useListEvaluationsQuery, type Evaluation } from "@/lib/store/apis/evaluationsApi";
import { useHarvestRedTeamMutation } from "@/lib/store/apis/ufl5Api";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Finished evaluations that ran a red-team benchmark, newest first. */
export function redTeamEvaluations(list: readonly Evaluation[] | undefined): Evaluation[] {
	return [...(list ?? [])]
		.filter((e) => e.status === "done" && (e.spec?.benchmarks ?? []).some((b) => b.startsWith("red-team")))
		.sort((a, b) => b.created_at.localeCompare(a.created_at));
}

/** Harvest the successful attacks of one finished red-team evaluation into pending regressions. */
export function HarvestForm() {
	const { t } = useTranslation("ufl5");
	const q = useListEvaluationsQuery({ status: "done", limit: 100 });
	const evals = redTeamEvaluations(q.data);
	const [pick, setPick] = useState("");
	const [harvest, st] = useHarvestRedTeamMutation();
	const [error, setError] = useState<unknown>(null);
	const chosen = pick || evals[0]?.id || "";
	if (q.isLoading || evals.length === 0) return null;
	return (
		<div className="flex flex-col gap-2 border-t pt-3 text-sm" data-testid="red-team-harvest">
			<div className="flex flex-wrap items-end gap-2">
				<label className="flex min-w-0 flex-col gap-1">
					<span className="text-muted-foreground text-xs">{t("harvest.evaluation")}</span>
					<NativeSelect value={chosen} onChange={(e) => setPick(e.target.value)} data-testid="red-team-harvest-eval">
						{evals.map((e) => (
							<option key={e.id} value={e.id}>
								{e.spec?.name || e.id}
							</option>
						))}
					</NativeSelect>
				</label>
				<Button
					size="sm"
					variant="outline"
					disabled={!chosen || st.isLoading}
					onClick={() => {
						setError(null);
						harvest(chosen)
							.unwrap()
							.catch((e: unknown) => setError(e));
					}}
					dataTestId="red-team-harvest-go"
				>
					{t("harvest.go")}
				</Button>
			</div>
			{st.data && !st.isLoading && (
				<p data-testid="red-team-harvest-done">{t("harvest.done", { added: st.data.added?.length ?? 0, broken: st.data.broken })}</p>
			)}
			<InlineError error={error} testId="red-team-harvest-error" />
		</div>
	);
}
