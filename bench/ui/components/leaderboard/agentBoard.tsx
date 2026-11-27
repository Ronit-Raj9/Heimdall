import { EmptyState } from "@/components/shell/emptyState";
import "@/components/reportcard/ufrbI18n";
import { LoadError, TableSkeleton } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { SortableHead } from "@/components/ui/sortableHead";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Toolbar } from "@/components/ui/toolbar";
import { useTableSort, type SortAccessors, type SortHeadProps, type TableSort, type TableSortDir } from "@/components/ui/useTableSort";
import { useGetAgentLeaderboardQuery, type AgentLeaderboardParams, type AgentLeaderboardRow } from "@/lib/store/apis/agentLeaderboardApi";
import { cn } from "@/lib/utils";
import { useNavigate } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useId, useMemo } from "react";
import { useTranslation } from "react-i18next";
import {
	ALL,
	boardRanks,
	DEFAULT_MIN_RUNS,
	DEFAULT_MIN_TASKS,
	filterParam,
	formatCostPerRun,
	formatShare,
	reportCardSearch,
	rowCost,
	rowKey,
	sameAgent,
	scoreState,
	showRange,
} from "./agentBoard.utils";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		agentBoard: typeof import("@/locales/en/agentBoard.json");
	}
}

/** Capabilities in display order (contract section 4). */
const CAPABILITY_ORDER = ["terminal", "coding", "memory", "support", "research", "voice", "safety", "team", "gpu"] as const;

/** The sort key of a capability's score column. */
export const scoreKey = (capability: string) => `score:${capability}`;

/** What each Agents table column sorts on: names as text, scores and cost as numbers (a cost not known sorts last). */
export function agentSortAccessors(capabilities: readonly string[]): SortAccessors<AgentLeaderboardRow, string> {
	const out: SortAccessors<AgentLeaderboardRow, string> = {
		agent: (r) => r.agent,
		version: (r) => (r.version > 0 ? r.version : null),
		model: (r) => r.model || null,
		cost: (r) => {
			const c = rowCost(r);
			return c.known && typeof r.cost_usd === "number" ? r.cost_usd : null;
		},
	};
	for (const c of capabilities) out[scoreKey(c)] = (r) => r.capabilities[c]?.score ?? null;
	return out;
}

/** First click on a column: best first. Scores and versions descend; names and cost ascend. */
export const agentFirstDir = (key: string): TableSortDir => (key.startsWith("score:") || key === "version" ? "desc" : "asc");

/** The Agents table keeps the server's order until a column is clicked. */
const AGENT_SORT_INITIAL: TableSort<string> = { key: null, dir: "asc" };

/** A capability's display name; an unknown one shows its id. */
function useCapabilityLabel() {
	const { t } = useTranslation("agentBoard");
	return (c: string) =>
		(CAPABILITY_ORDER as readonly string[]).includes(c) ? t(`capability.${c as (typeof CAPABILITY_ORDER)[number]}`) : c;
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
	const sortAccessors = useMemo(() => agentSortAccessors(data?.capabilities ?? []), [data?.capabilities]);
	const sorted = useTableSort(data?.rows ?? EMPTY_ROWS, AGENT_SORT_INITIAL, sortAccessors, { firstDir: agentFirstDir });
	const hasFilters = Boolean(params.capability || params.model);
	const reset = () => {
		void setCapability(null);
		void setModel(null);
	};
	const open = (row: AgentLeaderboardRow) =>
		void navigate({
			to: "/agents/$agentId",
			params: { agentId: row.agent },
			search: reportCardSearch(row) as never,
		});

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
			{/* The tab already says "Agents": the heading is for screen readers only, and the column definitions stay visible while the board loads. */}
			<section className="flex flex-col gap-3" data-testid="agent-board-ranking">
				<h2 className="sr-only">{t("title")}</h2>
				<p className="text-muted-foreground max-w-prose text-sm" data-testid="agent-board-description">
					{t("description", {
						runs: data?.min_runs ?? DEFAULT_MIN_RUNS,
						tasks: data?.min_tasks ?? DEFAULT_MIN_TASKS,
					})}
				</p>
				{board.isLoading ? (
					// The table frame shows at once while the board loads (it can take a few seconds); nothing else waits for it.
					<TableSkeleton rows={6} testId="agent-board-loading" />
				) : board.isError || !data ? (
					<LoadError title={t("loadError")} error={board.error} onRetry={() => void board.refetch()} testId="agent-board-error" />
				) : data.rows.length === 0 ? (
					// Q-154, Q-311: filtered empty offers Clear filters; an empty board leads to the wizard.
					hasFilters ? (
						<EmptyState kind="filtered" body={t("emptyFiltered")} action={{ label: t("resetFilters"), onClick: reset }} testId="agent-board-empty" />
					) : (
						<EmptyState body={t("empty")} action={{ label: t("evaluateAgent", { ns: "ufrb" }), to: "/evaluate" }} testId="agent-board-empty" />
					)
				) : (
					<div className={cn("bg-card min-w-0 overflow-x-auto rounded-xl border transition-opacity", board.isFetching && "opacity-60")}>
						<AgentBoardTable
							rows={sorted.rows}
							head={sorted.head}
							capabilities={data.capabilities}
							rankBy={params.capability}
							minRuns={data.min_runs}
							minTasks={data.min_tasks}
							onOpen={open}
						/>
					</div>
				)}
			</section>
		</section>
	);
}

const EMPTY_ROWS: AgentLeaderboardRow[] = [];

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
				<SelectTrigger id={id} size="sm" className={cn("min-w-32 text-xs", mono && value !== ALL && "font-mono")} data-testid={testId}>
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
	/** The capability rows are ranked by (the capability filter); without it rows rank by the All score. */
	rankBy?: string;
	minRuns?: number;
	minTasks?: number;
	/** Sort props of a column (agentSortAccessors keys); without it the headers do not sort. */
	head?: (key: string) => SortHeadProps;
}

/**
 * The board: rank (with a capability filter), agent, version, model, a score per capability with
 * its 95% range and sample, cost per run. A score under the minimum sample is shown but marked
 * "not enough runs" and never ranked; an unpriced cost is "not known", never $0.
 */
export function AgentBoardTable({ rows, capabilities, onOpen, rankBy, minRuns, minTasks, head }: AgentBoardTableProps) {
	const { t } = useTranslation("agentBoard");
	const capLabel = useCapabilityLabel();
	const mins = {
		minRuns: minRuns ?? DEFAULT_MIN_RUNS,
		minTasks: minTasks ?? DEFAULT_MIN_TASKS,
	};
	// Every row gets a place: by the filtered capability, else by the All score.
	const ranks = useMemo(() => boardRanks(rows, rankBy), [rows, rankBy]);
	const modelFirst = sameAgent(rows);
	const column = (key: string, label: string, testId: string, align: "left" | "right" = "left", className?: string, title?: string) =>
		head ? (
			<SortableHead key={key} label={label} align={align} testId={testId} className={className} hint={title} {...head(key)} />
		) : (
			<TableHead key={key} className={cn(align === "right" && "text-right", className)} title={title} data-testid={testId}>
				{label}
			</TableHead>
		);
	return (
		<Table data-testid="agent-board-table">
			<TableHeader>
				<TableRow>
					<TableHead className="w-12 text-right" title={rankBy ? undefined : t("rankAllHint")}>
						{t("columns.rank")}
					</TableHead>
					{column("agent", modelFirst ? t("columns.model") : t("columns.agent"), "agent-board-col-agent")}
					{column("version", t("columns.version"), "agent-board-col-version", "left", "hidden sm:table-cell")}
					{!modelFirst && column("model", t("columns.model"), "agent-board-col-model", "left", "hidden sm:table-cell")}
					{capabilities.map((c) =>
						column(scoreKey(c), t("scoreHeader", { capability: capLabel(c) }), `agent-board-col-${c}`, "right", undefined, t("scoreHint")),
					)}
					{column("cost", t("columns.cost"), "agent-board-col-cost", "right", "hidden sm:table-cell")}
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row) => {
					const place = ranks.get(rowKey(row));
					const version = row.version > 0 ? `v${row.version}` : t("noVersion");
					return (
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
							<TableCell className="text-right tabular-nums" data-testid="agent-board-rank" title={place?.tied ? t("tieHint") : undefined}>
								{place ? `${place.tied ? "=" : ""}${place.rank}` : <span className="text-muted-foreground">-</span>}
							</TableCell>
							<TableCell>
								<div className="flex flex-col gap-0.5">
									<div className="flex flex-wrap items-center gap-2">
										<span className="font-medium">{modelFirst ? row.model || "-" : row.agent}</span>
										{place?.best && (
											<Badge variant="success" data-testid="agent-board-best">
												{t("best")}
											</Badge>
										)}
										{!modelFirst && row.baseline && (
											<Badge variant="neutral" title={t("baselineHint")} data-testid="agent-board-baseline">
												{t("baseline")}
											</Badge>
										)}
										{row.customer && (
											<Badge
												variant="neutral"
												title={t("simulatedCustomerHint", {
													simulator: row.simulator ?? "",
												})}
												data-testid="agent-board-simulated-customer"
											>
												{t("simulatedCustomer")}
											</Badge>
										)}
									</div>
									{modelFirst ? (
										<span className="text-muted-foreground text-xs" data-testid="agent-board-agent-line">
											{row.agent} {version}
											{row.baseline && <span title={t("baselineHint")}> ({t("baseline")})</span>}
										</span>
									) : (
										<span className="text-muted-foreground text-xs sm:hidden" data-testid="agent-board-mobile-line">
											{version}, <span className="font-mono">{row.model || "-"}</span>
										</span>
									)}
								</div>
							</TableCell>
							<TableCell className="hidden font-mono text-xs sm:table-cell" title={row.version > 0 ? undefined : t("noVersionHint")}>
								{version}
							</TableCell>
							{!modelFirst && <TableCell className="hidden font-mono text-xs sm:table-cell">{row.model || "-"}</TableCell>}
							{capabilities.map((c) => (
								<TableCell key={c} className="text-right tabular-nums" data-testid={`agent-board-score-${c}`}>
									<ScoreCell s={row.capabilities[c]} mins={mins} />
								</TableCell>
							))}
							<TableCell className="hidden text-right text-xs tabular-nums sm:table-cell">
								<CostCell row={row} />
							</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}

/**
 * One capability score. No runs: "not measured". Under the minimum sample: "Too few runs (n)" in
 * muted text with the reason in the tooltip, never a percent or a range. Ranked: the share, a thin
 * bar with the 95% range as a lighter band, and the range only when its ends differ.
 */
function ScoreCell({
	s,
	mins,
}: {
	s: AgentLeaderboardRow["capabilities"][string] | undefined;
	mins: { minRuns: number; minTasks: number };
}) {
	const { t } = useTranslation("agentBoard");
	const state = scoreState(s);
	if (!s || state === "notMeasured") {
		return (
			<span className="text-muted-foreground text-xs" title={t("notMeasuredHint")} data-testid="agent-board-not-measured">
				{t("noScore")}
			</span>
		);
	}
	if (state === "tooFew") {
		return (
			<span
				className="text-muted-foreground text-xs"
				title={t("notEnoughHint", {
					runs: s.runs,
					tasks: s.tasks ?? 0,
					...mins,
				})}
				data-testid="agent-board-not-enough"
			>
				{t("tooFew", { runs: s.runs })}
			</span>
		);
	}
	const pct = (v: number) => `${Math.max(0, Math.min(100, v * 100))}%`;
	return (
		<span
			className="inline-flex min-w-20 flex-col items-end gap-0.5 leading-tight"
			title={t("range", {
				low: formatShare(s.ci_low),
				high: formatShare(s.ci_high),
				runs: s.runs,
				tasks: s.tasks ?? 0,
			})}
		>
			<span className="font-medium">{formatShare(s.score)}</span>
			<span className="bg-muted relative h-1 w-full overflow-hidden rounded" aria-hidden data-testid="agent-board-bar">
				<span className="bg-primary/30 absolute inset-y-0" style={{ left: pct(s.ci_low), width: pct(s.ci_high - s.ci_low) }} />
				<span className="bg-primary absolute inset-y-0 left-0 rounded" style={{ width: pct(s.score) }} />
			</span>
			{showRange(s.ci_low, s.ci_high) && (
				<span className="text-muted-foreground text-xs">
					{formatShare(s.ci_low)} to {formatShare(s.ci_high)}
				</span>
			)}
			<span className="text-muted-foreground text-xs">{t("sample", { runs: s.runs, tasks: s.tasks ?? 0 })}</span>
		</span>
	);
}

/** Cost per run, or "not known" with the count of unpriced runs (reason in the tooltip). */
function CostCell({ row }: { row: AgentLeaderboardRow }) {
	const { t } = useTranslation("agentBoard");
	const c = rowCost(row);
	if (c.known) return <>{t("perRun", { cost: formatCostPerRun(row.cost_usd) })}</>;
	const detail = t("costUnpriced", { count: c.unpriced, runs: c.runs });
	return (
		<span
			data-testid="agent-board-cost-unknown"
			title={c.note ? `${detail}: ${c.note}` : detail}
			className="inline-flex flex-col items-end leading-tight"
		>
			<span className="text-muted-foreground">{t("costNotKnown")}</span>
			{c.runs > 0 && <span className="text-muted-foreground text-xs">{detail}</span>}
		</span>
	);
}