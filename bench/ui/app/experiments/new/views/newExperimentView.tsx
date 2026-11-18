import { ExperimentProgressCard } from "@/components/experiments/experimentProgress";
import { RoleGate } from "@/components/platform/accessNotes";
import { TERMINAL_STATES } from "@/components/experiments/experiments.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Card, CardContent } from "@/components/ui/card";
import { EXPERIMENT_POLL_MS, useGetCatalogQuery, useGetExperimentQuery } from "@/lib/store/apis/benchApi";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ExperimentForm } from "./experimentForm";
import { dateSeed, defaultFormValues } from "./experimentFormSchema";

/** New experiment and preflight (DESIGN.md 18 screen 4). /experiments/new?exp=<id> shows that experiment's live progress. */
export default function NewExperimentView() {
	const { t } = useTranslation("bench");
	const [expId, setExpId] = useQueryState("exp", parseAsString);
	const [capUsd, setCapUsd] = useState<number | undefined>(undefined);
	const catalog = useGetCatalogQuery();
	const defaults = useMemo(() => (catalog.data ? defaultFormValues(catalog.data, dateSeed(new Date())) : null), [catalog.data]);

	return (
		<div className="flex flex-col gap-6" data-testid="new-experiment-screen">
			<header className="flex flex-col gap-1">
				<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("experiments.new.title")}</h1>
				<p className="text-muted-foreground text-sm">{t("experiments.new.subtitle")}</p>
			</header>

			{expId && <LiveProgress expId={expId} capUsd={capUsd} onNew={() => void setExpId(null)} />}

			{catalog.isLoading ? (
				<BlockSkeleton rows={10} testId="new-experiment-loading" />
			) : catalog.error || !catalog.data ? (
				<LoadError
					title={t("experiments.new.catalogError")}
					error={catalog.error}
					onRetry={() => void catalog.refetch()}
					testId="new-experiment-catalog-error"
				/>
			) : catalog.data.suites.length === 0 || catalog.data.agents.length === 0 || !defaults ? (
				<EmptyNote testId="new-experiment-catalog-empty">{t("experiments.new.catalogEmpty")}</EmptyNote>
			) : (
				// Hosted: starting experiments needs the member role; demo visitors see no form.
				<RoleGate min="member">
					<ExperimentForm
						catalog={catalog.data}
						defaults={defaults}
						onStarted={(id, cap) => {
							setCapUsd(cap);
							void setExpId(id);
						}}
					/>
				</RoleGate>
			)}
		</div>
	);
}

/** Polls GET /experiments/{id} every 2 s until the experiment reaches a terminal state. */
function LiveProgress({ expId, capUsd, onNew }: { expId: string; capUsd: number | undefined; onNew: () => void }) {
	const { t } = useTranslation("bench");
	const [finished, setFinished] = useState(false);
	const status = useGetExperimentQuery(expId, { pollingInterval: finished ? 0 : EXPERIMENT_POLL_MS, skipPollingIfUnfocused: true });
	const terminal = status.data ? TERMINAL_STATES.has(status.data.state) : false;

	useEffect(() => {
		setFinished(terminal);
	}, [terminal]);

	return (
		<Card className="py-4 shadow-none" data-testid="new-experiment-progress-card">
			<CardContent className="px-4">
				{status.isLoading ? (
					<BlockSkeleton rows={3} testId="experiment-progress-loading" />
				) : status.error || !status.data ? (
					<LoadError
						title={t("experiments.new.progress.loadError")}
						error={status.error}
						onRetry={() => void status.refetch()}
						testId="experiment-progress-error"
					/>
				) : (
					<div className="flex flex-col gap-1">
						<ExperimentProgressCard status={status.data} capUsd={capUsd} onNew={onNew} />
						<p className="text-muted-foreground text-[11px]">{t("experiments.new.progress.description")}</p>
					</div>
				)}
			</CardContent>
		</Card>
	);
}