import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Toolbar } from "@/components/ui/toolbar";
import {
	useGetAgentLeaderboardQuery,
	type AgentLeaderboardParams,
	type AgentLeaderboardRow,
} from "@/lib/store/apis/agentLeaderboardApi";
import { cn } from "@/lib/utils";
import { useNavigate } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useId, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { ALL, filterParam, formatCost, formatShare, reportCardSearch, rowKey } from "./agentBoard.utils";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		agentBoard: typeof import("@/locales/en/agentBoard.json");
	}
}

/** Capabilities in display order (contract section 4). */
const CAPABILITY_ORDER = ["terminal", "coding", "memory", "support", "research", "voice", "safety", "team", "gpu"] as const;

/** A capability's display name; an unknown one shows its id. */
function useCapabilityLabel() {
	const { t } = useTranslation("agentBoard");
	return (c: string) => ((CAPABILITY_ORDER as readonly string[]).includes(c) ? t(`capability.${c as (typeof CAPABILITY_ORDER)[number]}`) : c);
}

/**
 * The Agents tab of /leaderboard (default): one row per agent version and model, a score per
 * capability. ?capability=&model= filter on the server; a row opens the agent's report card.
 */
export function AgentBoardTab() {
	const { t } = useTranslation("agentBoard");
	const [capability, setCapability] = useQueryState("capability", parseAsString);
	const [model, setModel] = useQueryState("model", parseAsString);
	const params = useMemo<AgentLeaderboardParams>(
		() => ({ capability: filterParam(capability), model: filterParam(model) }),
		[capability, model],
	);
	const board = useGetAgentLeaderboardQuery(params);
	const navigate = useNavigate();
	const data = board.data;
	const hasFilters = Boolean(params.capability || params.model);
	const reset = () => {
		void setCapability(null);
		void setModel(null);
	};
	const open = (row: AgentLeaderboardRow) =>
		void navigate({ to: "/agents/$agentId", params: { agentId: row.agent }, search: reportCardSearch(row) as never });

	// The filter lists come from the unfiltered columns the server reports; a URL value they lack still shows.
	const capOptions = useMemo(() => {
		const list = [...(data?.capabilities ?? [])];
		if (params.capability && !list.includes(params.capability)) list.unshift(params.capability);
		return list;
	}, [data?.capabilities, params.capability]);
	const modelOptions = useMemo(() => {
		const list = [...(data?.models ?? [])];
		if (params.model && !list.includes(params.model)) list.unshift(params.model);
		return list;
	}, [data?.models, params.model]);

	return (
		<section className="flex min-w-0 flex-col gap-4" data-testid="agent-board">
			<AgentBoardToolbar
				capability={params.capability ?? ALL}
				capabilities={capOptions}
				model={params.model ?? ALL}
				models={modelOptions}
				onCapability={(v) => void setCapability(v === ALL ? null : v)}
				onModel={(v) => void setModel(v === ALL ? null : v)}
				onReset={hasFilters ? reset : undefined}
				disabled={board.isLoading}
			/>
			{board.isLoading ? (
				<BlockSkeleton className="h-64" testId="agent-board-loading" />
			) : board.isError || !data ? (
				<LoadError title={t("loadError")} error={board.error} onRetry={() => void board.refetch()} testId="agent-board-error" />
			) : (
				<Section title={t("title")} description={t("description")} testId="agent-board-ranking">
					{data.rows.length === 0 ? (
						<EmptyNote testId="agent-board-empty">{hasFilters ? t("emptyFiltered") : t("empty")}</EmptyNote>
					) : (
						<div className={cn("bg-card min-w-0 overflow-x-auto rounded-xl border transition-opacity", board.isFetching && "opacity-60")}>
							<AgentBoardTable rows={data.rows} capabilities={data.capabilities} onOpen={open} />
						</div>
					)}
				</Section>
			)}
		</section>
	);
}

export interface AgentBoardToolbarProps {
	capability: string;
	capabilities: readonly string[];
	model: string;
	models: readonly string[];
	onCapability: (v: string) => void;
	onModel: (v: string) => void;
	/** Shown only when a filter is set. */
	onReset?: () => void;
	disabled?: boolean;
}

/** Compact filter row: capability and model selects, and a clear button when filtered. */
export function AgentBoardToolbar(props: AgentBoardToolbarProps) {
	const { t } = useTranslation("agentBoard");
	const capLabel = useCapabilityLabel();
	return (
		<Toolbar aria-label={t("filters.label")} data-testid="agent-board-filters">
			<CompactSelect
				label={t("filters.capability")}
				value={props.capability}
				options={props.capabilities}
				format={capLabel}
				onChange={props.onCapability}
				disabled={props.disabled}
				testId="agent-board-capability"
			/>
			<CompactSelect
				label={t("filters.model")}
				value={props.model}
				options={props.models}
				onChange={props.onModel}
				disabled={props.disabled}
				testId="agent-board-model"
				mono
			/>
			{props.onReset && (
				<Button variant="ghost" size="sm" onClick={props.onReset} dataTestId="agent-board-reset">
					{t("resetFilters")}
				</Button>
			)}
		</Toolbar>
	);
}

function CompactSelect({
	label,
	value,
	options,
	format,
	onChange,
	disabled,
	testId,
	mono,
}: {
	label: string;
	value: string;
	options: readonly string[];
	format?: (v: string) => string;
	onChange: (v: string) => void;
	disabled?: boolean;
	testId: string;
	mono?: boolean;
}) {
	const { t } = useTranslation("agentBoard");
	const id = useId();
	return (
		<div className="flex items-center gap-2">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{label}
			</label>
			<Select value={value} onValueChange={onChange} disabled={disabled}>
				<SelectTrigger id={id} size="sm" className={cn("min-w-32 text-xs", mono && "font-mono")} data-testid={testId}>
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					<SelectItem value={ALL} className="text-xs">
						{t("filters.all")}
					</SelectItem>
					{options.map((o) => (
						<SelectItem key={o} value={o} className={cn("text-xs", mono && "font-mono")} data-testid={`${testId}-option-${o}`}>
							{format ? format(o) : o}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}

export interface AgentBoardTableProps {
	rows: readonly AgentLeaderboardRow[];
	/** Capability columns, in display order. */
	capabilities: readonly string[];
	onOpen: (row: AgentLeaderboardRow) => void;
}

/** The board: agent, version, model, a score per capability with its 95% range, cost per run. */
export function AgentBoardTable({ rows, capabilities, onOpen }: AgentBoardTableProps) {
	const { t } = useTranslation("agentBoard");
	const capLabel = useCapabilityLabel();
	return (
		<Table data-testid="agent-board-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("columns.agent")}</TableHead>
					<TableHead>{t("columns.version")}</TableHead>
					<TableHead>{t("columns.model")}</TableHead>
					{capabilities.map((c) => (
						<TableHead key={c} className="text-right" data-testid={`agent-board-col-${c}`}>
							{capLabel(c)}
						</TableHead>
					))}
					<TableHead className="text-right">{t("columns.cost")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row) => (
					<TableRow
						key={rowKey(row)}
						tabIndex={0}
						role="link"
						aria-label={t("openReportCard", { agent: row.agent })}
						className="hover:bg-muted/50 focus-visible:bg-muted/50 cursor-pointer focus-visible:outline-none"
						onClick={() => onOpen(row)}
						onKeyDown={(e) => {
							if (e.key === "Enter" || e.key === " ") {
								e.preventDefault();
								onOpen(row);
							}
						}}
						data-testid={`agent-board-row-${row.agent}`}
					>
						<TableCell>
							<div className="flex items-center gap-2">
								<span className="font-medium">{row.agent}</span>
								{row.baseline && (
									<Badge variant="neutral" title={t("baselineHint")} data-testid="agent-board-baseline">
										{t("baseline")}
									</Badge>
								)}
								{row.customer && (
									<Badge
										variant="neutral"
										title={t("simulatedCustomerHint", { simulator: row.simulator ?? "" })}
										data-testid="agent-board-simulated-customer"
									>
										{t("simulatedCustomer")}
									</Badge>
								)}
							</div>
						</TableCell>
						<TableCell className="font-mono text-xs">{row.version > 0 ? `v${row.version}` : t("noVersion")}</TableCell>
						<TableCell className="font-mono text-xs">{row.model || "-"}</TableCell>
						{capabilities.map((c) => {
							const s = row.capabilities[c];
							return (
								<TableCell key={c} className="text-right tabular-nums" data-testid={`agent-board-score-${c}`}>
									{s ? (
										<span
											className="inline-flex flex-col items-end leading-tight"
											title={t("range", { low: formatShare(s.ci_low), high: formatShare(s.ci_high), runs: s.runs })}
										>
											<span className="font-medium">{formatShare(s.score)}</span>
											<span className="text-muted-foreground text-[11px]">
												{formatShare(s.ci_low)} to {formatShare(s.ci_high)}
											</span>
										</span>
									) : (
										<span className="text-muted-foreground text-xs">{t("noScore")}</span>
									)}
								</TableCell>
							);
						})}
						<TableCell className="text-right font-mono text-xs tabular-nums">{formatCost(row.cost_usd)}</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}
