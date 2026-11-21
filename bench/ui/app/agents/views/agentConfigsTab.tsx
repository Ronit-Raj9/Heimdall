import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell";
import { Button } from "@/components/ui/button";
import type { AgentSummary, AgentsResponse, Job } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Bot, ChevronRight, Cpu, FlaskConical, Info, Play } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";
import { AgentAvatar, CheckPill, adapterIcon, isTestAgent, jobState, lastJobFor, useAdapterLabel } from "./agentDisplay";
import { useAgentJobs } from "./agentJobs";
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

/** Configs tab: every agent as a card; scripted test agents fold into a group at the bottom. */
export function AgentConfigsTab({ query }: { query: AgentsQuery }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("agentsUx");
	const [wanted] = useQueryState("agent", parseAsString);
	const jobs = useAgentJobs();
	const data = query.data;
	if (query.isLoading) return <BlockSkeleton rows={6} testId="agents-loading" />;
	if (query.isError || !data) {
		return <LoadError title={t("agents.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="agents-error" />;
	}
	if (data.agents.length === 0) return <EmptyState icon={Bot} message={t("agents.empty")} testId="agents-empty" />;
	const missing = wanted !== null && !data.agents.some((agent) => agentKey(agent) === wanted);
	const real = data.agents.filter((agent) => !isTestAgent(agent));
	const tests = data.agents.filter((agent) => isTestAgent(agent));
	return (
		<div className={cn("flex min-w-0 flex-col gap-6", query.isFetching && "opacity-60")}>
			{missing && (
				<p role="note" className="text-muted-foreground flex items-start gap-2 text-sm" data-testid="agents-missing">
					<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("agents.missing", { agent: wanted })}
				</p>
			)}
			{data.mode === "synthetic" && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-xl border px-4 py-2 text-sm"
					data-testid="agents-synthetic-banner"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("agents.synthetic")}
				</p>
			)}
			<ul aria-label={t("agents.label")} className="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3" data-testid="agents-list">
				{real.map((agent) => (
					<AgentListCard key={agent.path} agent={agent} jobs={jobs} selected={wanted === agentKey(agent)} />
				))}
			</ul>
			{tests.length > 0 && (
				<details className="group rounded-xl border" data-testid="agents-test-group">
					<summary className="hover:bg-muted/50 flex cursor-pointer list-none items-center gap-2 rounded-xl px-5 py-3 text-sm font-medium">
						<ChevronRight className="text-muted-foreground size-4 transition-transform group-open:rotate-90" aria-hidden="true" />
						<FlaskConical className="text-muted-foreground size-4" aria-hidden="true" />
						{tu("list.testAgents")}
						<span className="bg-muted text-muted-foreground rounded-full px-2 py-0.5 text-xs tabular-nums">{tests.length}</span>
						<span className="text-muted-foreground ml-2 hidden truncate text-xs font-normal sm:inline">{tu("list.testAgentsHint")}</span>
					</summary>
					<ul className="grid min-w-0 grid-cols-1 gap-4 p-5 pt-2 sm:grid-cols-2 xl:grid-cols-3">
						{tests.map((agent) => (
							<AgentListCard key={agent.path} agent={agent} jobs={jobs} selected={wanted === agentKey(agent)} />
						))}
					</ul>
				</details>
			)}
		</div>
	);
}

/** One agent: avatar, name, friendly adapter, default model, last check and a Run button. */
function AgentListCard({ agent, jobs, selected }: { agent: AgentSummary; jobs?: readonly Job[]; selected: boolean }) {
	const { t } = useTranslation("agentsUx");
	const adapterLabel = useAdapterLabel();
	const key = agentKey(agent);
	const name = agent.id || agent.path.replace(/^.*\//, "").replace(/\.ya?ml$/, "");
	const Icon = adapterIcon(agent.adapter);
	const state = agent.error ? "broken" : jobState(lastJobFor(jobs, "agent.check", agent.path, agent.id));
	const testId = `agents-row-${key.replace(/[^A-Za-z0-9_-]/g, "-")}`;
	return (
		<li
			className={cn(
				"bg-card group/card relative flex min-w-0 flex-col gap-4 rounded-xl border p-5 transition hover:-translate-y-0.5 hover:shadow-md",
				selected && "ring-primary/40 ring-2",
			)}
			aria-selected={selected || undefined}
			data-state={selected ? "selected" : undefined}
			data-testid={testId}
		>
			<div className="flex min-w-0 items-start gap-3">
				<AgentAvatar name={name} />
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<Link
						to="/agents/$agentId"
						params={{ agentId: key }}
						className="text-foreground truncate text-base font-semibold tracking-tight after:absolute after:inset-0 after:rounded-xl focus-visible:outline-none"
						aria-label={t("actions.open", { name })}
						title={name}
					>
						{name}
					</Link>
					<span className="text-muted-foreground flex items-center gap-1.5 text-sm">
						<Icon className="size-3.5" aria-hidden="true" />
						{adapterLabel(agent.adapter)}
					</span>
				</div>
			</div>
			<div className="flex min-w-0 flex-wrap items-center gap-2">
				{agent.model ? (
					<span className="bg-muted text-foreground inline-flex max-w-full items-center gap-1 truncate rounded-full px-2 py-0.5 font-mono text-xs" title={agent.model}>
						<Cpu className="text-muted-foreground size-3 shrink-0" aria-hidden="true" />
						<span className="truncate">{agent.model}</span>
					</span>
				) : (
					!agent.error && <span className="text-muted-foreground text-xs">{t("list.noModel")}</span>
				)}
				<CheckPill state={state} testId={`${testId}-status`} />
			</div>
			<div className="mt-auto flex items-center justify-between gap-2">
				<span className="text-muted-foreground text-xs tabular-nums">
					{agent.sealed_runs !== null && !agent.error ? t("list.runs", { count: agent.sealed_runs }) : ""}
				</span>
				{!agent.error && (
					<Button asChild size="sm" variant="outline" className="relative z-10">
						<Link to="/experiments/new" search={{ agent: key } as never} data-testid={`${testId}-run`}>
							<Play aria-hidden="true" />
							{t("actions.run")}
						</Link>
					</Button>
				)}
			</div>
		</li>
	);
}
