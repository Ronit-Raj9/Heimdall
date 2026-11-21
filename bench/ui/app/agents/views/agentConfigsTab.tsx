import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell";
import { Button } from "@/components/ui/button";
import type { AgentSummary, AgentsResponse, Job } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Bot, Cpu, Gauge, Info } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";
import "@/lib/types/agentFirstI18n";
import { AgentAvatar, CheckPill, adapterIcon, isBaselineAgent, isTestAgent, jobState, lastJobFor, useAdapterLabel } from "./agentDisplay";
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

/** The agents list split: the org's own agents, the seeded baselines, and test doubles (never shown). */
export function splitAgents(agents: readonly AgentSummary[]): { yours: AgentSummary[]; baselines: AgentSummary[]; tests: AgentSummary[] } {
	const tests = agents.filter((agent) => isTestAgent(agent));
	const real = agents.filter((agent) => !isTestAgent(agent));
	return { yours: real.filter((a) => !isBaselineAgent(a)), baselines: real.filter((a) => isBaselineAgent(a)), tests };
}

/** Configs tab: "Your agents" first, then "Baseline agents" for comparison. Test agents are hidden. */
export function AgentConfigsTab({ query }: { query: AgentsQuery }) {
	const { t } = useTranslation("bench");
	const { t: ta } = useTranslation("agentFirst");
	const [wanted] = useQueryState("agent", parseAsString);
	const jobs = useAgentJobs();
	const data = query.data;
	if (query.isLoading) return <BlockSkeleton rows={6} testId="agents-loading" />;
	if (query.isError || !data) {
		return <LoadError title={t("agents.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="agents-error" />;
	}
	if (data.agents.length === 0) return <EmptyState icon={Bot} message={t("agents.empty")} testId="agents-empty" />;
	const missing = wanted !== null && !data.agents.some((agent) => agentKey(agent) === wanted);
	const { yours, baselines } = splitAgents(data.agents);
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
			<section className="flex min-w-0 flex-col gap-3" data-testid="agents-yours" aria-labelledby="agents-yours-title">
				<h2 id="agents-yours-title" className="text-base font-semibold tracking-tight">
					{ta("list.yours")}
				</h2>
				{yours.length === 0 ? (
					<div className="text-muted-foreground flex flex-wrap items-center gap-3 rounded-xl border border-dashed px-5 py-4 text-sm" data-testid="agents-yours-empty">
						<span>{ta("list.yoursEmpty")}</span>
						<Button asChild size="sm">
							<Link to="/agents/new" data-testid="agents-yours-add">
								{ta("list.add")}
							</Link>
						</Button>
					</div>
				) : (
					<ul aria-label={t("agents.label")} className="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3" data-testid="agents-list">
						{yours.map((agent) => (
							<AgentListCard key={agent.path} agent={agent} jobs={jobs} selected={wanted === agentKey(agent)} />
						))}
					</ul>
				)}
			</section>
			{baselines.length > 0 && (
				<section className="flex min-w-0 flex-col gap-3" data-testid="agents-baselines" aria-labelledby="agents-baselines-title">
					<div className="flex flex-col gap-0.5">
						<h2 id="agents-baselines-title" className="text-base font-semibold tracking-tight">
							{ta("list.baselines")}
						</h2>
						<p className="text-muted-foreground text-sm">{ta("list.baselinesHint")}</p>
					</div>
					<ul className="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3" data-testid="agents-baseline-list">
						{baselines.map((agent) => (
							<AgentListCard key={agent.path} agent={agent} jobs={jobs} selected={wanted === agentKey(agent)} />
						))}
					</ul>
				</section>
			)}
		</div>
	);
}

/** One agent: avatar, name, friendly adapter, default model, last check and a Run button. */
function AgentListCard({ agent, jobs, selected }: { agent: AgentSummary; jobs?: readonly Job[]; selected: boolean }) {
	const { t } = useTranslation("agentsUx");
	const { t: ta } = useTranslation("agentFirst");
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
						<Link to={"/evaluate" as never} search={{ agent: key } as never} data-testid={`${testId}-run`}>
							<Gauge aria-hidden="true" />
							{ta("list.evaluate")}
						</Link>
					</Button>
				)}
			</div>
		</li>
	);
}
