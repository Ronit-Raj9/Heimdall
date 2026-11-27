import { isBaselineAgent, isTestAgent, AgentAvatar } from "@/app/agents/views/agentDisplay";
import { formatRelative } from "@/components/platform/platform.utils";
import { capabilityName, faultChange, faultName, pct, usd } from "@/components/reportcard/reportCard.utils";
import "@/app/evaluations/views/gw08I18n";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import { useGetAgentVersionsQuery, type Evaluation, type Report } from "@/lib/store/apis/evaluationsApi";
import { useGetAgentCardQuery } from "@/lib/store/apis/agentCardApi";
import { isTestObject } from "@/lib/testObjects";
import { preferredAgentEvaluation } from "./agentEvaluation";
import type { AgentSummary } from "@/lib/types/bench";
import "@/lib/types/agentFirstI18n";
import "./uf2aI18n";
import { Link } from "@tanstack/react-router";
import { Gauge, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Agent cards shown on Home. */
export const HOME_AGENTS_SHOWN = 6;

type EvaluationFacts = Pick<Evaluation, "spec" | "created_at"> & Partial<Pick<Evaluation, "id" | "status">>;

/**
 * The evaluations that give an agent a report card (Q-18): finished ones that are not test objects
 * ("zz-test-..." names). The tile and the cards both count agents by these, so they agree.
 */
export function scoredEvaluations<T extends EvaluationFacts>(evaluations: readonly T[]): T[] {
	return evaluations.filter((ev) => (ev.status === undefined || ev.status === "done") && !isTestObject(ev.spec?.name ?? ev.id));
}

/** When each agent was last in a finished, non-test evaluation (newest created_at over the evaluations' arms), as ms. */
export function lastEvaluated(evaluations: readonly EvaluationFacts[]): Map<string, number> {
	const last = new Map<string, number>();
	for (const ev of scoredEvaluations(evaluations)) {
		const at = Date.parse(ev.created_at ?? "");
		const t = Number.isFinite(at) ? at : 0;
		for (const arm of ev.spec?.arms ?? []) if (arm.agent && t >= (last.get(arm.agent) ?? -1)) last.set(arm.agent, t);
	}
	return last;
}

/**
 * The agents Home shows (M2-11, M1-19): the org's own agents plus built-in agents that were
 * evaluated here, never test doubles or broken configs; agents with an evaluation first, newest
 * evaluation first, then by name. At most n.
 */
export function ownAgents(
	agents: readonly AgentSummary[],
	n = HOME_AGENTS_SHOWN,
	evaluations: readonly EvaluationFacts[] = [],
): AgentSummary[] {
	const last = lastEvaluated(evaluations);
	return agents
		.filter((a) => !a.error && !isTestAgent(a) && (!isBaselineAgent(a) || last.has(a.id)))
		.map((a, i) => ({ a, i, at: last.get(a.id) }))
		.sort((x, y) => {
			if (x.at !== undefined && y.at !== undefined) return y.at - x.at || x.a.id.localeCompare(y.a.id);
			if (x.at !== undefined) return -1;
			if (y.at !== undefined) return 1;
			return x.i - y.i;
		})
		.slice(0, n)
		.map((x) => x.a);
}

/** How many agents get a report card on Home: the "Agents with results" tile counts the same set as the cards (M1-19, Q-18). */
export function agentsWithResults(agents: readonly AgentSummary[], evaluations: readonly EvaluationFacts[]): number {
	const last = lastEvaluated(evaluations);
	return ownAgents(agents, Number.POSITIVE_INFINITY, evaluations).filter((a) => last.has(a.id)).length;
}

/** Built-in agents suggested when fewer than two agents have results (M2-11). */
export const SUGGESTED_BUILTINS = ["codex", "claude-code"] as const;

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

/**
 * "Your agents" on Home: one card per agent with results; agents without one as a short list
 * (M2-12); a pointer to the built-in agents while few have results; a way to add the first agent.
 */
export function YourAgents({
	agents,
	evaluations,
	mayRun,
}: {
	agents: readonly AgentSummary[];
	evaluations: readonly Evaluation[];
	mayRun: boolean;
}) {
	const { t } = useTranslation("agentFirst");
	const { t: tf } = useTranslation("uf2a");
	const own = ownAgents(agents, HOME_AGENTS_SHOWN, evaluations);
	const last = lastEvaluated(evaluations);
	const withResults = own.filter((a) => last.has(a.id));
	const without = own.filter((a) => !last.has(a.id));
	const builtins = SUGGESTED_BUILTINS.filter((id) => !last.has(id) && agents.some((a) => a.id === id));
	const footer = (
		<div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
			{mayRun && withResults.length < 2 && builtins.length > 0 && (
				<span data-testid="home-agents-builtins">
					{tf("home.tryBuiltin")}{" "}
					{builtins.map((id, i) => (
						<span key={id}>
							{i > 0 && ", "}
							<Link
								to={"/evaluate" as never}
								search={{ agent: id } as never}
								className="text-foreground font-medium underline-offset-4 hover:underline"
								data-testid={`home-agents-builtin-${id}`}
							>
								{id}
							</Link>
						</span>
					))}
				</span>
			)}
			<Link
				to={"/scheduled-checks" as never}
				className="hover:text-foreground ml-auto font-medium underline-offset-4 hover:underline"
				data-testid="home-agents-scheduled"
			>
				{tf("home.scheduled")}
			</Link>
		</div>
	);
	if (own.length === 0) {
		return (
			<div className="flex min-w-0 flex-col gap-3">
				<div
					className="bg-card text-muted-foreground flex flex-wrap items-center gap-3 rounded-xl border border-dashed px-5 py-4 text-sm"
					data-testid="home-agents-empty"
				>
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
				{footer}
			</div>
		);
	}
	return (
		<div className="flex min-w-0 flex-col gap-3">
			{withResults.length > 0 && (
				<ul className="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3" data-testid="home-agents-list">
					{withResults.map((a) => (
						<AgentCard key={a.id} agent={a} evaluations={evaluations} mayRun={mayRun} />
					))}
				</ul>
			)}
			{without.length > 0 && (
				<div className="bg-card min-w-0 rounded-xl border" data-testid="home-agents-unevaluated">
					{withResults.length === 0 && (
						<p className="text-muted-foreground border-b px-4 py-3 text-sm">{tf("home.noneEvaluated", { count: without.length })}</p>
					)}
					<ul className="divide-y">
						{without.map((a) => (
							<li key={a.id} className="relative flex min-w-0 items-center gap-3 px-4 py-2.5" data-testid={`home-agent-${a.id}`}>
								<AgentAvatar name={a.id} />
								<div className="flex min-w-0 flex-1 flex-col">
									<Link
										to="/agents/$agentId"
										params={{ agentId: a.id }}
										className="truncate text-sm font-medium after:absolute after:inset-0 focus-visible:outline-none"
									>
										{a.id}
									</Link>
									<span className="text-muted-foreground truncate text-xs" data-testid={`home-agent-${a.id}-none`}>
										{[a.model, t("home.notEvaluated")].filter(Boolean).join(" · ")}
									</span>
								</div>
								{mayRun && (
									<Button asChild size="sm" variant="ghost" className="relative z-10 shrink-0">
										<Link to={"/evaluate" as never} search={{ agent: a.id } as never} data-testid={`home-agent-${a.id}-evaluate`}>
											<Gauge aria-hidden="true" />
											{t("home.evaluate")}
										</Link>
									</Button>
								)}
							</li>
						))}
					</ul>
				</div>
			)}
			{footer}
		</div>
	);
}

function AgentCard({ agent, evaluations, mayRun }: { agent: AgentSummary; evaluations: readonly Evaluation[]; mayRun: boolean }) {
	const { t } = useTranslation("agentFirst");
	const { t: tg } = useTranslation("gw08");
	const { t: tf } = useTranslation("uf2a");
	const locale = getLocale();
	// Q-17: the same evaluation the agent page's Report card tab opens on (never a test object).
	const first = useGetAgentCardQuery({ agent: agent.id });
	const pick = first.data ? preferredAgentEvaluation(first.data) : undefined;
	const picked = useGetAgentCardQuery({ agent: agent.id, evaluation: pick ?? "" }, { skip: !pick });
	const report = pick ? picked : first;
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
				<span
					className="bg-muted text-muted-foreground ml-auto shrink-0 rounded-md px-1.5 py-0.5 font-mono text-xs"
					data-testid={`${testId}-version`}
				>
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
								<span className="text-muted-foreground">{capabilityName(c.id)}</span>{" "}
								<span className="font-semibold tabular-nums">{pct(c.score)}</span>
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
					<Button asChild size="sm" variant="ghost">
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