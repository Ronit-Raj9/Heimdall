import { AgentCard } from "@/components/agents/agentCard";
import { AgentVersions } from "@/components/agents/agentVersions";
import { CompareVersionsLink } from "@/components/agentdiff/agentDiffLinks";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, EntityLink } from "@/components/shell";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { AgentSummary, AgentsResponse } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { AlertTriangle, Bot, CircleX, EyeOff, Info } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

/** What the agents tabs read of GET /agents. */
export interface AgentsQuery {
	data?: AgentsResponse;
	isLoading: boolean;
	isFetching: boolean;
	isError: boolean;
	error?: unknown;
	refetch: () => unknown;
}

/** The id an agent row is known by: its config id, or its file when the config failed to load. */
export function agentKey(agent: AgentSummary): string {
	return agent.id || agent.path;
}

/** The config ?agent= names, else the first one that loaded. */
export function pickAgent(agents: readonly AgentSummary[], wanted: string | null): AgentSummary | null {
	return agents.find((agent) => agentKey(agent) === wanted) ?? agents.find((agent) => !agent.error) ?? agents[0] ?? null;
}

/** Configs tab: every config in a table, the one in ?agent= open beside it with its versions. */
export function AgentConfigsTab({ query }: { query: AgentsQuery }) {
	const { t } = useTranslation("bench");
	const [wanted] = useQueryState("agent", parseAsString);
	const data = query.data;
	if (query.isLoading) return <BlockSkeleton rows={6} testId="agents-loading" />;
	if (query.isError || !data) {
		return <LoadError title={t("agents.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="agents-error" />;
	}
	if (data.agents.length === 0) return <EmptyState icon={Bot} message={t("agents.empty")} testId="agents-empty" />;
	const open = pickAgent(data.agents, wanted);
	const missing = wanted !== null && !data.agents.some((agent) => agentKey(agent) === wanted);
	return (
		<div className="flex min-w-0 flex-col gap-4">
			{missing && (
				<p role="note" className="text-muted-foreground flex items-start gap-2 text-sm" data-testid="agents-missing">
					<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("agents.missing", { agent: wanted })}
				</p>
			)}
			{data.mode === "synthetic" && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-sm border px-4 py-2 text-sm"
					data-testid="agents-synthetic-banner"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("agents.synthetic")}
				</p>
			)}
			<div
				className={cn(
					"grid min-w-0 grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,30rem)]",
					query.isFetching && "opacity-60",
				)}
			>
				<div className="min-w-0 rounded-sm border">
					<Table aria-label={t("agents.label")} data-testid="agents-list">
						<TableHeader>
							<TableRow>
								<TableHead>{t("agents.table.config")}</TableHead>
								<TableHead>{t("agents.table.adapter")}</TableHead>
								<TableHead>{t("agents.table.evidence")}</TableHead>
								<TableHead className="text-right">{t("agents.table.runs")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{data.agents.map((agent) => {
								const key = agentKey(agent);
								const selected = open !== null && agentKey(open) === key;
								return (
									<TableRow
										key={agent.path}
										data-state={selected ? "selected" : undefined}
										aria-selected={selected}
										data-testid={`agents-row-${key.replace(/[^A-Za-z0-9_-]/g, "-")}`}
									>
										<TableCell className="font-medium">
											<EntityLink kind="agent" id={key} />
										</TableCell>
										{agent.error ? (
											<TableCell colSpan={3}>
												<ToneBadge tone="bad" icon={CircleX}>
													{t("agents.loadFailed")}
												</ToneBadge>
											</TableCell>
										) : (
											<>
												<TableCell>
													{agent.scripted_kind ? (
														<ToneBadge tone="warn" hint={t("agents.scriptedHint")}>
															{t("agents.scripted", { kind: agent.scripted_kind })}
														</ToneBadge>
													) : (
														agent.adapter
													)}
												</TableCell>
												<TableCell>
													{agent.parser ? t("agents.table.native", { parser: agent.parser }) : t("agents.table.gateway")}
												</TableCell>
												<TableCell className="text-right tabular-nums">{agent.sealed_runs ?? t("agents.runsUnknown")}</TableCell>
											</>
										)}
									</TableRow>
								);
							})}
						</TableBody>
					</Table>
				</div>
				{open && <AgentDetail key={agentKey(open)} agent={open} />}
			</div>
			<p className="text-muted-foreground flex items-start gap-2 text-xs" role="note" data-testid="agents-secret-note">
				<EyeOff className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
				{t("agents.secretNote")}
			</p>
		</div>
	);
}

/** One config: the card, then the versions the run folder knows with a link to compare them. */
function AgentDetail({ agent }: { agent: AgentSummary }) {
	return (
		<div className="flex min-w-0 flex-col gap-4 xl:sticky xl:top-4" data-testid="agents-detail">
			<AgentCard agent={agent} />
			{!agent.error && <AgentVersions agentId={agent.id} actions={<CompareVersionsLink agent={agent.id} className="h-7 px-2 text-xs" />} />}
		</div>
	);
}