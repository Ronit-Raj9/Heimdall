import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatCount } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { AgentStats } from "@/lib/types/bench";
import { clipText } from "@/components/runs/stepTimeline.utils";
import { useTranslation } from "react-i18next";
import { AgentId, RoleChip, UnknownValue } from "./agentBadges";
import { formatDurationMs, formatUsd4 } from "./agents.utils";

/**
 * What each agent did and cost: steps, model turns, tool calls, tokens, cost, messages and busy
 * time, one row per agent in tree order, the agent column pinned while the table scrolls. Tokens
 * and cost need gateway rows with the agent's id: when the run did not record them they read
 * "unknown", never zero.
 */
export function AgentTable({ members, arm }: { members: readonly AgentStats[]; arm: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const count = (n: number) => formatCount(n, locale);
	const known = (value: number | null | undefined, format: (n: number) => string, testId: string) =>
		value === null || value === undefined || !Number.isFinite(value) ? (
			<UnknownValue testId={testId} />
		) : (
			<span data-testid={testId}>{format(value)}</span>
		);

	return (
		<Table stickyColumns={1} data-testid={`agents-table-${arm}`} containerClassName="rounded-lg border">
			<TableHeader>
				<TableRow>
					<TableHead>{t("runs.agents.table.agent")}</TableHead>
					<TableHead>{t("runs.agents.table.role")}</TableHead>
					<TableHead>{t("runs.agents.table.model")}</TableHead>
					<TableHead className="text-right">{t("runs.agents.table.steps")}</TableHead>
					<TableHead className="text-right">{t("runs.agents.table.modelTurns")}</TableHead>
					<TableHead className="text-right">{t("runs.agents.table.toolCalls")}</TableHead>
					<TableHead className="text-right">{t("runs.agents.table.inputTokens")}</TableHead>
					<TableHead className="text-right">{t("runs.agents.table.outputTokens")}</TableHead>
					<TableHead className="text-right">{t("runs.agents.table.cost")}</TableHead>
					<TableHead className="text-right">{t("runs.agents.table.sent")}</TableHead>
					<TableHead className="text-right">{t("runs.agents.table.received")}</TableHead>
					<TableHead className="text-right">{t("runs.agents.table.busy")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{members.map((m) => {
					const id = `agents-row-${arm}-${m.agent_id}`;
					return (
						<TableRow key={m.agent_id} data-testid={id}>
							<TableCell>
								<span className="inline-flex items-center" style={{ paddingLeft: `${Math.min(m.depth, 8) * 0.75}rem` }}>
									<AgentId id={m.agent_id} className="text-xs font-medium" />
								</span>
							</TableCell>
							<TableCell>
								{m.role ? <RoleChip role={m.role} /> : <span className="text-muted-foreground text-xs">{t("runs.agents.notSet")}</span>}
							</TableCell>
							<TableCell className="text-muted-foreground font-mono text-xs">
								{m.model ? <span title={clipText(m.model, 120)}>{clipText(m.model, 40)}</span> : t("runs.agents.notSet")}
							</TableCell>
							<TableCell className="text-right">{count(m.steps)}</TableCell>
							<TableCell className="text-right">{count(m.model_turns)}</TableCell>
							<TableCell className="text-right">{count(m.tool_calls)}</TableCell>
							<TableCell className="text-right">{known(m.input_tokens, count, `${id}-input`)}</TableCell>
							<TableCell className="text-right">{known(m.output_tokens, count, `${id}-output`)}</TableCell>
							<TableCell className="text-right">{known(m.cost_usd, (v) => formatUsd4(v, locale), `${id}-cost`)}</TableCell>
							<TableCell className="text-right">{count(m.messages_sent)}</TableCell>
							<TableCell className="text-right">{count(m.messages_received)}</TableCell>
							<TableCell className="text-right">{formatDurationMs(m.busy_ms, locale)}</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}