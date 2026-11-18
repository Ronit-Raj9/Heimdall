import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { clipText } from "@/components/runs/stepTimeline.utils";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { formatCount } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import { useGetRunAgentsQuery } from "@/lib/store/apis/runAgentsApi";
import type { RunAgents } from "@/lib/types/bench";
import { AlertTriangle, Network } from "lucide-react";
import { useTranslation } from "react-i18next";
import { AgentSwimlane } from "./agentSwimlane";
import { AgentTable } from "./agentTable";
import { AgentTree } from "./agentTree";
import { BlamePanel } from "./blamePanel";
import { CoverageBadge } from "./coverageBadge";
import { HandoffList } from "./handoffList";

const SHAPES = ["team", "in_process"] as const;

/**
 * One run's agents, fetched by run id: loading, error and the single-agent state are all shown,
 * never a blank box.
 */
export function RunAgentsPanel({ runId, arm }: { runId: string; arm: string }) {
	const { t } = useTranslation("bench");
	const agents = useGetRunAgentsQuery(runId);
	if (agents.isLoading) return <BlockSkeleton rows={4} testId={`agents-${arm}-loading`} />;
	if (agents.error || !agents.data) {
		return (
			<LoadError
				title={t("runs.agents.loadError")}
				error={agents.error}
				onRetry={() => void agents.refetch()}
				testId={`agents-${arm}-error`}
			/>
		);
	}
	return <AgentsPanel data={agents.data} arm={arm} />;
}

export interface AgentsPanelProps {
	data: RunAgents;
	arm: string;
	/** Handoff indexes open at first render. */
	initialExpanded?: readonly number[];
}

/**
 * The agents of a multi-agent run: who is blamed for the failure and how strongly that is known,
 * a swimlane per agent with the critical path, the agent tree and per-agent table, and every
 * handoff with what was lost on the way. A single-agent run says so instead.
 */
export function AgentsPanel({ data, arm, initialExpanded }: AgentsPanelProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	if (!data.available) {
		return <EmptyNote testId={`agents-${arm}-unavailable`}>{t("runs.agents.unavailable")}</EmptyNote>;
	}
	const coverage = data.tree?.coverage ?? data.blame?.coverage;
	const shape = data.tree?.shape;
	const knownShape = SHAPES.find((s) => s === shape);
	const agentCount = data.members.length || data.tree?.agents.length || 0;

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid={`agents-${arm}`}>
			<div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
				<ToneBadge tone="neutral" icon={Network} testId={`agents-${arm}-shape`}>
					{knownShape ? t(`runs.agents.shape.${knownShape}`) : shape ? clipText(shape, 24) : t("runs.agents.shape.unknown")}
				</ToneBadge>
				<span className="text-foreground text-sm font-medium" data-testid={`agents-${arm}-count`}>
					{t("runs.agents.count", { count: agentCount, value: formatCount(agentCount, locale) })}
				</span>
				{coverage ? (
					<CoverageBadge coverage={coverage} testId={`agents-${arm}-coverage`} />
				) : (
					<span className="text-muted-foreground text-xs">{t("runs.agents.coverage.missing")}</span>
				)}
			</div>
			{data.truncated && (
				<p className="text-chart-warning-ink flex items-center gap-1.5 text-xs" data-testid={`agents-${arm}-truncated`}>
					<AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
					{t("runs.agents.truncated")}
				</p>
			)}

			{data.blame ? (
				<BlamePanel blame={data.blame} arm={arm} />
			) : (
				<EmptyNote testId={`agents-${arm}-no-blame`} className="py-4">
					{t("runs.agents.blame.none")}
				</EmptyNote>
			)}

			<SubSection title={t("runs.agents.swimlane.title")} description={t("runs.agents.swimlane.description")}>
				<AgentSwimlane
					spans={data.spans}
					members={data.members}
					wallMs={data.wall_ms}
					busyMs={data.busy_ms}
					criticalMs={data.critical_ms}
					handoffs={data.handoffs}
					arm={arm}
				/>
			</SubSection>

			<SubSection title={t("runs.agents.tree.title")} description={t("runs.agents.tree.description")}>
				<AgentTree members={data.members} tree={data.tree} arm={arm} />
				{data.members.length > 0 && <AgentTable members={data.members} arm={arm} />}
			</SubSection>

			<SubSection title={t("runs.agents.handoffs.title")} description={t("runs.agents.handoffs.description")}>
				<HandoffList handoffs={data.handoffs} arm={arm} initialExpanded={initialExpanded} />
			</SubSection>

			{data.tree?.notes && data.tree.notes.length > 0 && (
				<div className="flex flex-col gap-1" data-testid={`agents-${arm}-notes`}>
					<h3 className="text-muted-foreground text-xs font-medium">{t("runs.agents.notes")}</h3>
					<ul className="text-muted-foreground list-disc pl-5 text-xs leading-relaxed">
						{data.tree.notes.map((note, i) => (
							// Notes can repeat, so the position is part of the key.
							<li key={`${i}-${note}`} className="break-words">
								{clipText(note, 400)}
							</li>
						))}
					</ul>
				</div>
			)}
		</div>
	);
}

function SubSection({ title, description, children }: { title: string; description: string; children: React.ReactNode }) {
	return (
		<div className="flex min-w-0 flex-col gap-2.5">
			<div className="flex flex-col gap-0.5">
				<h3 className="text-foreground text-[13px] font-semibold tracking-tight">{title}</h3>
				<p className="text-muted-foreground max-w-prose text-xs leading-relaxed">{description}</p>
			</div>
			{children}
		</div>
	);
}