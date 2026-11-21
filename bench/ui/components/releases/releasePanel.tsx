import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { sortExperimentsNewestFirst } from "@/components/verdict/verdict.utils";
import { getBenchErrorMessage, useCreateReleaseMutation, useGetVerdictQuery, useListExperimentsQuery } from "@/lib/store";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ReleaseForm } from "./releaseForm";
import { releasableExperiments, verdictPolicyDecision } from "./releases.utils";

/**
 * The "Release" panel: pick an experiment, see its verdict action and policy decision, and record a
 * release citing that exact analysis. POST /releases carries the write token from the shared header helper.
 */
export function ReleasePanel({ initialExpId }: { initialExpId?: string } = {}) {
	const { t } = useTranslation("bench");
	const list = useListExperimentsQuery();
	const experiments = useMemo(() => releasableExperiments(sortExperimentsNewestFirst(list.data?.experiments ?? [])), [list.data]);
	const [picked, setPicked] = useState<string | undefined>(initialExpId);
	const expId = picked ?? experiments[0]?.exp_id;
	const verdict = useGetVerdictQuery(expId ?? "", { skip: !expId });
	const [createRelease, created] = useCreateReleaseMutation();
	const data = verdict.data;

	return (
		<Card className="gap-4 py-4 shadow-none" data-testid="release-panel">
			<CardHeader className="px-4">
				<CardTitle className="text-sm">{t("releases.panel.title")}</CardTitle>
				<CardDescription className="text-xs">{t("releases.panel.description")}</CardDescription>
			</CardHeader>
			<CardContent className="px-4">
				{list.isError && (
					<p role="alert" className="text-chart-error-ink mb-3 text-sm" data-testid="release-experiments-error">
						{t("releases.panel.experimentsError", { error: getBenchErrorMessage(list.error) })}
					</p>
				)}
				{!list.isLoading && experiments.length === 0 && !list.isError && (
					<p className="text-muted-foreground mb-3 text-sm" data-testid="release-no-experiments">
						{t("releases.panel.noExperiments")}
					</p>
				)}
				<ReleaseForm
					key={expId ?? "none"}
					experiments={experiments}
					expId={expId}
					onExpChange={(id) => {
						created.reset();
						setPicked(id);
					}}
					verdict={data ? { analysisId: data.analysis_id, decision: data.verdict.decision, action: data.verdict.action } : undefined}
					policy={verdictPolicyDecision(data)}
					verdictLoading={verdict.isFetching && !data}
					verdictError={verdict.isError ? getBenchErrorMessage(verdict.error) : undefined}
					submitting={created.isLoading}
					submitError={created.isError ? getBenchErrorMessage(created.error) : undefined}
					created={created.data}
					onSubmit={(request) => void createRelease(request)}
				/>
			</CardContent>
		</Card>
	);
}