import { EmptyNote } from "@/components/runs/screenStates";
import { patternName } from "./triageTable";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { TriageGroup, Value } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { parseTriageReason, patternRuns, type RunLocation } from "./triage.utils";

/** Event ids shown before "+N more"; a reason lists at most 10. */
const MAX_SHOWN_EVENTS = 10;

export interface TriageDrillDownProps {
	group: TriageGroup;
	arms: readonly string[];
	/** Run id -> pair, task, condition and trial, from the grid. */
	locations: ReadonlyMap<string, RunLocation>;
	/** The triggering events of one run. The screen fetches the run's metrics; tests pass data. */
	renderEvidence: (runId: string) => React.ReactNode;
	onClose: () => void;
}

/** The runs showing one pattern, each with a link to its matched pair and the events that show it. */
export function TriageDrillDown({ group, arms, locations, renderEvidence, onClose }: TriageDrillDownProps) {
	const { t } = useTranslation("bench");
	const { t: af } = useTranslation("agentFirst");
	const runs = patternRuns(group, arms);
	// The same name as the pattern's row and the By agent version tab (RH).
	const name = patternName(group, (key) => (af(`failures.pattern.${key}` as never, { defaultValue: "" }) as string) || undefined);

	return (
		<section id="triage-drilldown" className="flex flex-col gap-3" data-testid={`triage-drilldown-${group.pattern}`}>
			<div className="flex flex-wrap items-start justify-between gap-2">
				<div className="flex flex-col gap-0.5">
					<h2 className="text-foreground text-sm font-semibold tracking-tight">{t("triage.drill.title", { pattern: name })}</h2>
				</div>
				<Button variant="ghost" size="sm" onClick={onClose} dataTestId="triage-drilldown-close" aria-label={t("triage.drill.close")}>
					<X className="size-4" aria-hidden="true" />
					{t("triage.drill.close")}
				</Button>
			</div>
			{runs.length === 0 ? (
				<EmptyNote testId="triage-drilldown-empty">{t("triage.drill.empty")}</EmptyNote>
			) : (
				<Table data-testid="triage-drilldown-table">
					<TableHeader>
						<TableRow>
							<TableHead>{t("triage.drill.run")}</TableHead>
							<TableHead>{t("triage.drill.arm")}</TableHead>
							<TableHead>{t("triage.drill.task")}</TableHead>
							<TableHead>{t("triage.drill.events")}</TableHead>
							<TableHead />
						</TableRow>
					</TableHeader>
					<TableBody>
						{runs.map(({ arm, runId }) => {
							const where = locations.get(runId);
							return (
								<TableRow key={runId} data-testid={`triage-run-${runId}`}>
									<TableCell className="align-top font-mono text-xs">{runId}</TableCell>
									<TableCell className="align-top font-semibold">{arm}</TableCell>
									<TableCell className="align-top text-xs">
										{where ? (
											<span className="flex flex-col">
												<span className="font-mono">{where.task}</span>
												<span className="text-muted-foreground">
													{where.condition} · {t("triage.drill.trial", { trial: where.trial })}
												</span>
											</span>
										) : (
											<span className="text-muted-foreground">-</span>
										)}
									</TableCell>
									<TableCell className="max-w-lg align-top whitespace-normal">{renderEvidence(runId)}</TableCell>
									<TableCell className="text-right align-top">
										{where ? (
											<Link
												to="/runs"
												search={{ pair: where.pairId }}
												data-testid={`triage-compare-${runId}`}
												className="text-primary text-xs whitespace-nowrap underline-offset-4 hover:underline"
											>
												{t("triage.drill.compare")}
											</Link>
										) : (
											<span className="text-muted-foreground text-xs" data-testid={`triage-nopair-${runId}`}>
												{t("triage.drill.noPair")}
											</span>
										)}
									</TableCell>
								</TableRow>
							);
						})}
					</TableBody>
				</Table>
			)}
		</section>
	);
}

export interface TriageRunEvidenceProps {
	runId: string;
	/** The run's value for the pattern metric, once loaded. */
	value: Value | undefined;
	isLoading: boolean;
	isError: boolean;
}

/** One run's triggering event ids and the detector's one-line summary, from `Value.Reason`. */
export function TriageRunEvidence({ runId, value, isLoading, isError }: TriageRunEvidenceProps) {
	const { t } = useTranslation("bench");
	if (isLoading) return <span className="text-muted-foreground text-xs">{t("triage.drill.loading")}</span>;
	if (isError) return <span className="text-muted-foreground text-xs">{t("triage.drill.loadError")}</span>;
	const { eventIds, summary } = parseTriageReason(value?.reason);
	const shown = eventIds.slice(0, MAX_SHOWN_EVENTS);
	return (
		<div className="flex flex-col gap-1" data-testid={`triage-evidence-${runId}`}>
			{shown.length === 0 ? (
				<span className="text-muted-foreground text-xs">{t("triage.drill.noReason")}</span>
			) : (
				<ul className="flex flex-wrap gap-1" aria-label={t("triage.drill.events")}>
					{shown.map((id) => (
						<li key={id}>
							<code className="bg-muted text-foreground rounded-sm px-1 py-0.5 font-mono text-[11px]" data-testid="triage-event-id">
								{id}
							</code>
						</li>
					))}
					{eventIds.length > shown.length && (
						<li className="text-muted-foreground text-[11px]">{t("triage.drill.moreEvents", { count: eventIds.length - shown.length })}</li>
					)}
				</ul>
			)}
			{summary && <p className="text-muted-foreground text-xs break-words">{summary}</p>}
		</div>
	);
}