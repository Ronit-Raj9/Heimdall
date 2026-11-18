import { clipText } from "@/components/runs/stepTimeline.utils";
import type { AgentStats, AgentTree as AgentTreeData } from "@/lib/types/bench";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { AgentId, ExitBadge, RoleChip, StrengthBadge } from "./agentBadges";
import { treeOrder } from "./agents.utils";

/** What one tree row shows, from the per-agent stats with the tree node's name added. */
interface TreeRow {
	id: string;
	parentId?: string;
	role?: string;
	name?: string;
	model?: string;
	source?: string;
	strength?: string;
	exit?: string;
}

function treeRows(members: readonly AgentStats[], tree: AgentTreeData | undefined): TreeRow[] {
	const nodes = new Map((tree?.agents ?? []).map((node) => [node.id, node]));
	if (members.length > 0) {
		return members.map((m) => ({
			id: m.agent_id,
			parentId: m.parent_id,
			role: m.role,
			name: nodes.get(m.agent_id)?.name,
			model: m.model,
			source: m.source,
			strength: m.strength,
			exit: m.exit,
		}));
	}
	return (tree?.agents ?? []).map((n) => ({
		id: n.id,
		parentId: n.parent_id,
		role: n.role,
		name: n.name,
		model: n.model,
		source: n.source,
		strength: n.strength,
		exit: n.exit,
	}));
}

const INDENT_REM = 1.5;

/**
 * Who the agents were and who started whom: depth first by parent id, each child indented under
 * its parent with an elbow guide. Each row shows the role, model, how the agent is known
 * (proven, declared or reported) and how it ended. Ids, names, roles and models are clipped text.
 */
export function AgentTree({ members, tree, arm }: { members: readonly AgentStats[]; tree?: AgentTreeData; arm: string }) {
	const { t } = useTranslation("bench");
	const entries = useMemo(
		() =>
			treeOrder(
				treeRows(members, tree),
				(row) => row.id,
				(row) => row.parentId,
			),
		[members, tree],
	);
	if (entries.length === 0) return null;
	return (
		<ul className="flex min-w-0 flex-col" aria-label={t("runs.agents.tree.label", { arm })} data-testid={`agents-tree-${arm}`}>
			{entries.map(({ node, depth, orphan }) => (
				<li
					key={node.id}
					className="relative min-w-0 py-1"
					style={{ paddingLeft: `${depth * INDENT_REM}rem` }}
					data-testid={`agents-tree-${arm}-${node.id}`}
					data-depth={depth}
				>
					{depth > 0 && (
						// The elbow from the parent's column into this row.
						<span
							aria-hidden="true"
							className="border-border absolute -top-1 h-[1.45rem] w-[0.9rem] rounded-bl-md border-b border-l"
							style={{ left: `${(depth - 1) * INDENT_REM + 0.6}rem` }}
						/>
					)}
					<div className="bg-card flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 rounded-md border px-2.5 py-1.5 text-xs shadow-(--shadow-card)">
						<AgentId id={node.id} className="text-[12px] font-semibold" />
						{node.name && node.name !== node.id && <span className="text-muted-foreground">{clipText(node.name, 48)}</span>}
						<RoleChip role={node.role} testId={`agents-tree-${arm}-${node.id}-role`} />
						{node.model && (
							<span className="text-muted-foreground font-mono text-[11px] break-all" title={clipText(node.model, 120)}>
								{clipText(node.model, 48)}
							</span>
						)}
						<span className="ml-auto flex flex-wrap items-center gap-1.5">
							<StrengthBadge strength={node.strength} source={node.source} testId={`agents-tree-${arm}-${node.id}-strength`} />
							<ExitBadge exit={node.exit} testId={`agents-tree-${arm}-${node.id}-exit`} />
						</span>
						{orphan && <span className="text-chart-warning-ink w-full text-[11px]">{t("runs.agents.tree.orphan")}</span>}
					</div>
				</li>
			))}
		</ul>
	);
}