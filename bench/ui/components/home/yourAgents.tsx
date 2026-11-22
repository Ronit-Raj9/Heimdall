import { isBaselineAgent, isTestAgent, AgentAvatar } from "@/app/agents/views/agentDisplay";
import { formatRelative } from "@/components/platform/platform.utils";
import { capabilityName, faultChange, faultName, pct, usd } from "@/components/reportcard/reportCard.utils";
import "@/app/evaluations/views/gw08I18n";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import { useGetAgentReportCardQuery, useGetAgentVersionsQuery, type Evaluation, type Report } from "@/lib/store/apis/evaluationsApi";
import type { AgentSummary } from "@/lib/types/bench";
import "@/lib/types/agentFirstI18n";
import { Link } from "@tanstack/react-router";
import { Gauge, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Agent cards shown on Home. */
export const HOME_AGENTS_SHOWN = 6;

/** The org's own agents (not baselines, not test doubles), in list order, at most n. */
export function ownAgents(agents: readonly AgentSummary[], n = HOME_AGENTS_SHOWN): AgentSummary[] {
	return agents.filter((a) => !a.error && !isTestAgent(a) && !isBaselineAgent(a)).slice(0, n);
}

export interface AgentCardStats {
	capabilities: { id: string; score: number }[];
	/** Dollars per solved run over the report's benchmarks; null when nothing was solved or no cost was seen. */
	costPerSolved: number | null;
	/** The fault with the largest loss from a clean run (most negative change), or null when no fault lowered the score. */
	worstDrop: { fault: string; drop: number } | null;
	/** True when the report has any fault result for this agent. */
	faultsTested: boolean;
}

/** What one Home card shows, from an agent's report card (its first arm). */
export function agentCardStats(report: Report | undefined): AgentCardStats {
	const label = report?.arms?.[0]?.label;
	if (!report || !label) return { capabilities: [], costPerSolved: null, worstDrop: null, faultsTested: false };
	const capabilities = (report.capabilities ?? [])
		.map((c) => ({ id: String(c.capability), score: c.arms?.[label]?.score }))
		.filter((c): c is { id: string; score: number } => typeof c.score === "number");
	let cost = 0;
	let solved = 0;
	let priced = false;
	for (const b of report.benchmarks ?? []) {
		const m = b.arms?.[label];
		if (!m) continue;
		if (typeof m.cost_usd === "number" && m.cost_usd > 0) priced = true;
		cost += (m.cost_usd ?? 0) * m.runs;
		solved += m.score * m.runs;
	}
	// drop is the change from clean (negative: the fault hurt, faultChange), so the worst is the smallest below 0.
	let worstDrop: AgentCardStats["worstDrop"] = null;
	let faultsTested = false;
	for (const r of report.reliability ?? []) {
		const d = r.arms?.[label]?.drop;
		if (typeof d !== "number") continue;
		faultsTested = true;
		if (faultChange({ drop: d }).hurt && (worstDrop === null || d < worstDrop.drop)) worstDrop = { fault: r.fault, drop: d };
	}
	return { capabilities, costPerSolved: priced && solved > 0 ? cost / solved : null, worstDrop, faultsTested };
}

/** "Your agents" on Home: one card per own agent, or a way to add the first one. */
export function YourAgents({ agents, evaluations, mayRun }: { agents: readonly AgentSummary[]; evaluations: readonly Evaluation[]; mayRun: boolean }) {
	const { t } = useTranslation("agentFirst");
	const own = ownAgents(agents);
	if (own.length === 0) {
		return (
			<div className="bg-card text-muted-foreground flex flex-wrap items-center gap-3 rounded-xl border border-dashed px-5 py-4 text-sm" data-testid="home-agents-empty">
				<span>{t("home.empty")}</span>
				{mayRun && (
					<Button asChild size="sm">
						<Link to="/agents/new" data-testid="home-agents-add">
							<Plus aria-hidden="true" />
							{t("home.add")}
						</Link>
					</Button>
				)}
			</div>
		);
	}
	return (
		<ul className="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3" data-testid="home-agents-list">
			{own.map((a) => (
				<AgentCard key={a.id} agent={a} evaluations={evaluations} mayRun={mayRun} />
			))}
		</ul>
	);
}

function AgentCard({ agent, evaluations, mayRun }: { agent: AgentSummary; evaluations: readonly Evaluation[]; mayRun: boolean }) {
	const { t } = useTranslation("agentFirst");
	const { t: tg } = useTranslation("gw08");
	const locale = getLocale();
	const report = useGetAgentReportCardQuery({ agent: agent.id });
	const versions = useGetAgentVersionsQuery(agent.id);
	const list = versions.data?.versions ?? [];
	const version = list.length > 0 ? Math.max(...list.map((v) => v.version)) : null;
	const stats = agentCardStats(report.data);
	const evaluationId = report.data?.evaluation || "";
	const evaluation = evaluations.find((e) => e.id === evaluationId);
	const testId = `home-agent-${agent.id}`;
	return (
		<li className="bg-card relative flex min-w-0 flex-col gap-4 rounded-xl border p-5 shadow-(--shadow-card)" data-testid={testId}>
			<div className="flex min-w-0 items-center gap-3">
				<AgentAvatar name={agent.id} />
				<Link
					to="/agents/$agentId"
					params={{ agentId: agent.id }}
					className="truncate text-base font-semibold tracking-tight after:absolute after:inset-0 after:rounded-xl focus-visible:outline-none"
				>
					{agent.id}
				</Link>
				<span className="bg-muted text-muted-foreground ml-auto shrink-0 rounded-md px-1.5 py-0.5 font-mono text-xs" data-testid={`${testId}-version`}>
					{version === null ? t("detail.noVersion") : `v${version}`}
				</span>
			</div>
			{report.isLoading ? (
				<div className="bg-muted h-16 animate-pulse rounded-lg" aria-hidden="true" />
			) : !evaluationId ? (
				<p className="text-muted-foreground text-sm" data-testid={`${testId}-none`}>
					{t("home.notEvaluated")}
				</p>
			) : (
				<>
					<ul className="flex flex-wrap gap-1.5" aria-label={t("home.scores")} data-testid={`${testId}-scores`}>
						{stats.capabilities.map((c) => (
							<li key={c.id} className="bg-muted/60 rounded-md border px-2 py-0.5 text-xs">
								<span className="text-muted-foreground">{capabilityName(c.id)}</span> <span className="font-semibold tabular-nums">{pct(c.score)}</span>
							</li>
						))}
					</ul>
					<dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
						<div className="flex flex-col gap-0.5">
							<dt className="text-muted-foreground">{t("home.costPerSolved")}</dt>
							<dd className="font-medium tabular-nums" data-testid={`${testId}-cost`}>
								{stats.costPerSolved === null ? t("home.unknown") : usd(stats.costPerSolved)}
							</dd>
						</div>
						<div className="flex flex-col gap-0.5">
							<dt className="text-muted-foreground">{t("home.faultDrop")}</dt>
							<dd className="font-medium tabular-nums" data-testid={`${testId}-drop`}>
								{stats.worstDrop !== null
									? t("home.drop", { points: faultChange(stats.worstDrop).text, fault: faultName(stats.worstDrop.fault) })
									: stats.faultsTested
										? tg("home.noDrop")
										: t("home.notTested")}
							</dd>
						</div>
					</dl>
				</>
			)}
			<div className="relative z-10 mt-auto flex items-center justify-between gap-2">
				{evaluationId ? (
					<Link
						to="/evaluations/$evalId"
						params={{ evalId: evaluationId }}
						className="text-muted-foreground hover:text-foreground truncate text-xs"
						data-testid={`${testId}-last`}
					>
						{t("home.lastEvaluation", { when: (evaluation && formatRelative(evaluation.created_at, locale)) || evaluationId })}
					</Link>
				) : (
					<span />
				)}
				{mayRun && (
					<Button asChild size="sm" variant="outline">
						<Link to={"/evaluate" as never} search={{ agent: agent.id } as never} data-testid={`${testId}-evaluate`}>
							<Gauge aria-hidden="true" />
							{t("home.evaluate")}
						</Link>
					</Button>
				)}
			</div>
		</li>
	);
}
