import { CatalogEmptyLinks } from "@/components/experiments/catalogEmptyLinks";
import { ExperimentProgressCard } from "@/components/experiments/experimentProgress";
import { RoleGate } from "@/components/platform/accessNotes";
import { TERMINAL_STATES } from "@/components/experiments/experiments.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { EXPERIMENT_POLL_MS, useGetCatalogQuery, useGetExperimentQuery, useGetHealthQuery } from "@/lib/store/apis/benchApi";
import { launchAvailable, type ExperimentStatus } from "@/lib/types/bench";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ExperimentForm } from "./experimentForm";
import { dateSeed, defaultFormValues } from "./experimentFormSchema";
import { MatrixBuilder } from "./matrixBuilder";
import { defaultBuilderValues } from "./matrixBuilderSchema";

/** New experiment and preflight (DESIGN.md 18 screen 4). /experiments/new?exp=<id> shows that experiment's live progress. */
export default function NewExperimentView() {
	const { t } = useTranslation("bench");
	const { t: tb } = useTranslation("builder");
	const [expId, setExpId] = useQueryState("exp", parseAsString);
	// ?mode=ab opens the A/B config-change form; the default is the agents x models builder.
	const [mode, setMode] = useQueryState("mode", parseAsString);
	const abMode = mode === "ab";
	const [capUsd, setCapUsd] = useState<number | undefined>(undefined);
	const catalog = useGetCatalogQuery();
	const health = useGetHealthQuery();
	const defaults = useMemo(() => (catalog.data ? defaultFormValues(catalog.data, dateSeed(new Date())) : null), [catalog.data]);
	const builderDefaults = useMemo(
		() => (catalog.data ? defaultBuilderValues(catalog.data, dateSeed(new Date())) : null),
		[catalog.data],
	);
	const onStarted = (id: string, cap: number) => {
		setCapUsd(cap);
		void setExpId(id);
	};

	return (
		<div className="flex flex-col gap-6" data-testid="new-experiment-screen">
			<PageHeader
				breadcrumbs={[{ label: t("experiments.list.title"), to: "/experiments" }, { label: t("experiments.new.title") }]}
				title={t("experiments.new.title")}
				subtitle={t("experiments.new.subtitle")}
			/>

			{expId && <LiveProgress key={expId} expId={expId} capUsd={capUsd} onNew={() => void setExpId(null)} />}

			{!launchAvailable(health.data) ? (
				<EmptyNote testId="new-experiment-offline">{t("experiments.new.offline")}</EmptyNote>
			) : catalog.isLoading ? (
				<BlockSkeleton rows={10} testId="new-experiment-loading" />
			) : catalog.error || !catalog.data ? (
				<LoadError
					title={t("experiments.new.catalogError")}
					error={catalog.error}
					onRetry={() => void catalog.refetch()}
					testId="new-experiment-catalog-error"
				/>
			) : catalog.data.suites.length === 0 || catalog.data.agents.length === 0 || !defaults ? (
				<CatalogEmptyLinks
					message={t("experiments.new.catalogEmpty")}
					noSuites={catalog.data.suites.length === 0}
					noAgents={catalog.data.agents.length === 0}
				/>
			) : (
				// Hosted: starting experiments needs the member role; demo visitors see no form.
				<RoleGate min="member">
					<div className="flex flex-col gap-4">
						<div className="flex flex-wrap items-center gap-2" role="tablist" aria-label={tb("mode.label")} data-testid="builder-mode">
							<Button
								type="button"
								size="sm"
								variant={abMode ? "outline" : "default"}
								role="tab"
								aria-selected={!abMode}
								onClick={() => void setMode(null)}
								dataTestId="builder-mode-matrix"
							>
								{tb("mode.matrix")}
							</Button>
							<Button
								type="button"
								size="sm"
								variant={abMode ? "default" : "outline"}
								role="tab"
								aria-selected={abMode}
								onClick={() => void setMode("ab")}
								dataTestId="builder-mode-ab"
							>
								{tb("mode.ab")}
							</Button>
							{abMode && <span className="text-muted-foreground text-xs">{tb("mode.abHint")}</span>}
						</div>
						{abMode || !builderDefaults ? (
							<ExperimentForm catalog={catalog.data} defaults={defaults} onStarted={onStarted} />
						) : (
							<MatrixBuilder catalog={catalog.data} defaults={builderDefaults} onStarted={onStarted} />
						)}
					</div>
				</RoleGate>
			)}
		</div>
	);
}

/** The progress card's polling: none once terminal, slower while created, else the live pace. */
export function progressPollMs(state: ExperimentStatus["state"] | undefined): number {
	if (state && TERMINAL_STATES.has(state)) return 0;
	return state === "created" ? CREATED_POLL_MS : EXPERIMENT_POLL_MS;
}

/** How often a created, unstarted experiment is checked for a start made elsewhere. */
export const CREATED_POLL_MS = 10_000;

/** Polls GET /experiments/{id} every 2 s until the experiment reaches a terminal state. */
function LiveProgress({ expId, capUsd, onNew }: { expId: string; capUsd: number | undefined; onNew: () => void }) {
	const { t } = useTranslation("bench");
	// A created experiment (its start failed) is idle until someone starts it, here or from another
	// tab or the CLI: keep watching it at a slower pace so that start shows (QC2-27-04). Stop only
	// once it is terminal.
	const [state, setState] = useState<ExperimentStatus["state"] | undefined>(undefined);
	const status = useGetExperimentQuery(expId, { pollingInterval: progressPollMs(state), skipPollingIfUnfocused: true });
	useEffect(() => {
		setState(status.data?.state);
	}, [status.data?.state]);

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