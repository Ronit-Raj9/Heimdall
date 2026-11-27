import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { ShowTestsChip } from "@/components/ui/toolbar";
import { isTestObject, useShowTests, withoutTests } from "@/lib/testObjects";
import { formatRelative } from "@/components/platform/platform.utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { AgentSummary, AgentsResponse, Job } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Bot, ChevronRight, Gauge, Info, MoreHorizontal, Search, Trash2 } from "lucide-react";
import { parseAsString, parseAsStringLiteral, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";
import "@/lib/types/agentFirstI18n";
import "@/components/home/uf2aI18n";
import "./ufreI18n";
import { useConfirm } from "@/components/platform/confirmDialog";
import { useSession } from "@/lib/session/sessionContext";
import { useDeleteAgentMutation } from "@/lib/store/apis/agentDeleteApi";
import { useState } from "react";
import {
	AGENT_SORTS,
	AgentAvatar,
	CheckPill,
	adapterKey,
	agentLastScore,
	agentRunsTotal,
	agentVersions,
	formatLastScore,
	filterAgents,
	isBaselineAgent,
	isTestAgent,
	jobState,
	lastJobFor,
	sortAgents,
	useAdapterLabel,
	type AgentSort,
} from "./agentDisplay";
import { useAgentJobs, useAgentJobsError } from "./agentJobs";
import { AgentRowDelete, agentRowDeletable } from "./agentRowDelete";
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

/**
 * The agents the list shows (M1-18): test agents and test objects ("zz-test-..." ids, test-double
 * descriptions) are hidden unless "Show test items" is on; `hidden` is how many were left out.
 */
export function visibleAgents(
	agents: readonly AgentSummary[],
	showTests: boolean,
): { yours: AgentSummary[]; baselines: AgentSummary[]; hidden: number } {
	const { shown, hidden } = withoutTests(agents, (a) => isTestAgent(a) || isTestObject(a.id || a.path), showTests);
	return { yours: shown.filter((a) => !isBaselineAgent(a)), baselines: shown.filter((a) => isBaselineAgent(a)), hidden };
}

/** How many agents the Configs tab shows (its own and the baselines; test agents are hidden): the tab badge. */
export function configsCount(agents: readonly AgentSummary[]): number {
	const { yours, baselines } = visibleAgents(agents, false);
	return yours.length + baselines.length;
}

/** The agent types present in the list, in a stable order, for the type chips (M2-26). */
export function agentTypes(agents: readonly Pick<AgentSummary, "adapter">[]): string[] {
	return [...new Set(agents.map((a) => adapterKey(a.adapter)))].sort();
}

/** Whether the baseline group opens by itself: on a search or type filter, with no own agents, or when a baseline has results (Q-124). */
export function baselinesOpen(baselines: readonly AgentSummary[], filtered: boolean, yoursCount: number): boolean {
	return filtered || yoursCount === 0 || baselines.some((a) => agentLastScore(a) !== null || (a.sealed_runs ?? 0) > 0);
}

/** The ids of the picked rows still in the list (a filter can hide a picked row; it is never deleted unseen). */
export function pickedInList(picked: ReadonlySet<string>, agents: readonly AgentSummary[]): string[] {
	return agents.filter((a) => a.id && picked.has(a.id)).map((a) => a.id);
}

/** Configs tab: "Your agents" first, then "Baseline agents" for comparison. Test agents are hidden. */
export function AgentConfigsTab({ query }: { query: AgentsQuery }) {
	const { t } = useTranslation("bench");
	const { t: ta } = useTranslation("agentFirst");
	const { t: tu } = useTranslation("agentsUx");
	const [wanted] = useQueryState("agent", parseAsString);
	const [q, setQ] = useQueryState("q", parseAsString.withDefault(""));
	const [sort, setSort] = useQueryState("sort", parseAsStringLiteral(AGENT_SORTS).withDefault("name"));
	const [type, setType] = useQueryState("type", parseAsString.withDefault(""));
	const { t: tf } = useTranslation("uf2a");
	const { t: tr } = useTranslation("ufre");
	const [showTests, setShowTests] = useShowTests();
	const [picked, setPicked] = useState<ReadonlySet<string>>(new Set());
	const session = useSession();
	const jobs = useAgentJobs();
	const jobsError = useAgentJobsError();
	const data = query.data;
	if (query.isLoading) return <BlockSkeleton rows={6} testId="agents-loading" />;
	if (query.isError || !data) {
		return <LoadError title={t("agents.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="agents-error" />;
	}
	if (data.agents.length === 0)
		return (
			<EmptyState
				icon={Bot}
				title={tf("agents.emptyTitle")}
				body={tf("agents.emptyBody")}
				action={{ label: tf("agents.fromTemplate"), to: "/agents/new" }}
				secondary={{ label: tf("agents.connect"), to: "/connect" }}
				testId="agents-empty"
			/>
		);
	const missing = wanted !== null && !data.agents.some((agent) => agentKey(agent) === wanted);
	const all = visibleAgents(data.agents, showTests);
	const types = agentTypes([...all.yours, ...all.baselines]);
	const ofType = (list: AgentSummary[]) => (type ? list.filter((a) => adapterKey(a.adapter) === type) : list);
	const yours = sortAgents(ofType(filterAgents(all.yours, q)), sort);
	const baselines = sortAgents(ofType(filterAgents(all.baselines, q)), sort);
	const noMatch = (q.trim() !== "" || type !== "") && yours.length === 0 && baselines.length === 0;
	const deletable = yours.filter((a) => !a.error && agentRowDeletable(session, a));
	const selection: Selection | undefined =
		deletable.length > 0
			? {
					picked,
					deletable: new Set(deletable.map((a) => a.id)),
					toggle: (id, on) =>
						setPicked((prev) => {
							const next = new Set(prev);
							if (on) next.add(id);
							else next.delete(id);
							return next;
						}),
					setAll: (on) => setPicked(on ? new Set(deletable.map((a) => a.id)) : new Set()),
				}
			: undefined;
	const pickedIds = pickedInList(picked, deletable);
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
					className="border-warning bg-warning-soft text-foreground flex items-start gap-2 rounded-xl border px-4 py-2 text-sm"
					data-testid="agents-synthetic-banner"
				>
					<AlertTriangle className="text-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("agents.synthetic")}
				</p>
			)}
			{jobsError && (
				<p role="note" className="text-muted-foreground flex items-start gap-2 text-sm" data-testid="agents-jobs-error">
					<AlertTriangle className="text-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{tf("agents.jobsError")}
				</p>
			)}
			<div className="flex min-w-0 flex-wrap items-center gap-2" data-testid="agents-controls">
				<div className="relative min-w-0 flex-1 sm:max-w-sm">
					<Search
						className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2"
						aria-hidden="true"
					/>
					<Input
						type="search"
						value={q}
						onChange={(e) => void setQ(e.target.value || null)}
						placeholder={tu("list.searchPlaceholder")}
						aria-label={tu("list.search")}
						className="pl-8"
						data-testid="agents-search"
					/>
				</div>
				<span className="text-muted-foreground text-sm" id="agents-sort-label">
					{tf("agents.sortBy")}
				</span>
				<Select value={sort} onValueChange={(next) => void setSort(next === "name" ? null : (next as AgentSort))}>
					<SelectTrigger className="w-40" aria-labelledby="agents-sort-label" data-testid="agents-sort">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value="name">{tu("list.sortName")}</SelectItem>
						<SelectItem value="last">{tu("list.sortLast")}</SelectItem>
						<SelectItem value="runs">{tu("list.sortRuns")}</SelectItem>
						<SelectItem value="score">{tr("agents.sortScore")}</SelectItem>
					</SelectContent>
				</Select>
				<ShowTestsChip count={all.hidden} on={showTests} onChange={setShowTests} />
				{pickedIds.length > 0 && <BulkDelete ids={pickedIds} onDone={(left) => setPicked(new Set(left))} />}
			</div>
			{types.length > 1 && (
				<div className="flex min-w-0 flex-wrap items-center gap-1.5" role="group" aria-label={tf("agents.typeLabel")} data-testid="agents-types">
					{["", ...types].map((k) => (
						<button
							key={k || "all"}
							type="button"
							aria-pressed={type === k}
							onClick={() => void setType(k || null)}
							className={cn(
								"min-h-8 rounded-full border px-3 text-sm transition-colors focus-visible:ring-2 focus-visible:ring-(--focus) focus-visible:outline-none",
								type === k ? "border-primary bg-primary/5 text-foreground font-medium" : "text-muted-foreground hover:border-input",
							)}
							data-testid={`agents-type-${k || "all"}`}
						>
							{k ? tf(`agents.type.${k as "command"}`) : tf("agents.typeAll")}
						</button>
					))}
				</div>
			)}
			{noMatch ? (
				<p
					className="text-muted-foreground flex flex-wrap items-center gap-1 rounded-xl border border-dashed px-5 py-4 text-sm"
					data-testid="agents-no-match"
				>
					<span>{tu("list.noMatch")}</span>
					<button
						type="button"
						className="text-primary underline-offset-2 hover:underline"
						onClick={() => {
							void setQ(null);
							void setType(null);
						}}
						data-testid="agents-clear-search"
					>
						{tu("list.clearSearch")}
					</button>
				</p>
			) : (
				<>
					<section className="flex min-w-0 flex-col gap-3" data-testid="agents-yours" aria-labelledby="agents-yours-title">
						<h2 id="agents-yours-title" className="text-lg font-semibold tracking-tight">
							{ta("list.yours")} <span className="text-muted-foreground font-normal tabular-nums">({yours.length})</span>
						</h2>
						{yours.length === 0 && q.trim() === "" ? (
							<div
								className="text-muted-foreground flex flex-wrap items-center gap-3 rounded-xl border border-dashed px-5 py-4 text-sm"
								data-testid="agents-yours-empty"
							>
								<span>{ta("list.yoursEmpty")}</span>
								<Button asChild size="sm">
									<Link to="/agents/new" data-testid="agents-yours-add">
										{ta("list.add")}
									</Link>
								</Button>
							</div>
						) : (
							<AgentTable label={t("agents.label")} agents={yours} jobs={jobs} wanted={wanted} testId="agents-list" selection={selection} />
						)}
					</section>
					{baselines.length > 0 && (
						<section className="flex min-w-0 flex-col gap-3" data-testid="agents-baselines" aria-labelledby="agents-baselines-title">
							<details className="group min-w-0" open={baselinesOpen(baselines, q.trim() !== "" || type !== "", yours.length)}>
								<summary className="flex min-h-8 cursor-pointer list-none flex-col gap-0.5" data-testid="agents-baselines-summary">
									<h2 id="agents-baselines-title" className="flex items-center gap-1.5 text-lg font-semibold tracking-tight">
										<ChevronRight
											className="text-muted-foreground size-4 shrink-0 transition-transform group-open:rotate-90 motion-reduce:transition-none"
											aria-hidden="true"
										/>
										{ta("list.baselines")}{" "}
										<span className="text-muted-foreground font-normal tabular-nums">({baselines.length})</span>
									</h2>
									<p className="text-muted-foreground pl-5.5 text-sm">{ta("list.baselinesHint")}</p>
								</summary>
								<div className="mt-3">
									<AgentTable label={ta("list.baselines")} agents={baselines} jobs={jobs} wanted={wanted} testId="agents-baseline-list" />
								</div>
							</details>
						</section>
					)}
				</>
			)}
		</div>
	);
}

/**
 * Agents as table rows (M2-21): name, how it runs, model, scored runs, last run, check state and
 * actions. The name link covers the row, so the whole row opens the agent. Phones get two-line
 * rows: name and state, then model and last run.
 */
interface Selection {
	picked: ReadonlySet<string>;
	/** Ids of the rows that may be deleted (own agents, the caller's role allows it). */
	deletable: ReadonlySet<string>;
	toggle(id: string, on: boolean): void;
	setAll(on: boolean): void;
}

function AgentTable({
	label,
	agents,
	jobs,
	wanted,
	testId,
	selection,
}: {
	label: string;
	agents: readonly AgentSummary[];
	jobs?: readonly Job[];
	wanted: string | null;
	testId: string;
	selection?: Selection;
}) {
	const { t: tf } = useTranslation("uf2a");
	const { t: tr } = useTranslation("ufre");
	const pickedCount = selection ? [...selection.deletable].filter((id) => selection.picked.has(id)).length : 0;
	return (
		<div className="bg-card min-w-0 overflow-hidden rounded-xl border">
			{/* Phones: a fixed layout, so the name takes the width the dot and actions leave (Q-2). */}
			<table className="w-full table-fixed text-sm md:table-auto" aria-label={label}>
				<thead className="bg-muted/40 text-muted-foreground hidden text-left text-xs md:table-header-group">
					<tr className="border-b">
						{selection && (
							<th className="w-10 py-2 pl-4 font-medium">
								<input
									type="checkbox"
									className="accent-primary size-4 cursor-pointer"
									checked={pickedCount > 0 && pickedCount === selection.deletable.size}
									ref={(el) => {
										if (el) el.indeterminate = pickedCount > 0 && pickedCount < selection.deletable.size;
									}}
									onChange={(e) => selection.setAll(e.target.checked)}
									aria-label={tr("agents.selectAll")}
									data-testid={`${testId}-select-all`}
								/>
							</th>
						)}
						<th className="px-4 py-2 font-medium">{tf("agents.columns.name")}</th>
						<th className="px-4 py-2 font-medium">{tf("agents.columns.runsAs")}</th>
						<th className="px-4 py-2 font-medium">{tf("agents.columns.model")}</th>
						<th className="hidden px-4 py-2 font-medium lg:table-cell">{tr("agents.columns.versions")}</th>
						<th className="hidden px-4 py-2 font-medium lg:table-cell">{tf("agents.columns.runs")}</th>
						<th className="hidden px-4 py-2 font-medium lg:table-cell">{tr("agents.columns.lastScore")}</th>
						<th className="hidden px-4 py-2 font-medium lg:table-cell">{tf("agents.columns.lastRun")}</th>
						<th className="px-4 py-2 font-medium">{tf("agents.columns.check")}</th>
						<th className="px-4 py-2 text-right font-medium">
							<span className="sr-only">{tf("agents.columns.actions")}</span>
						</th>
					</tr>
				</thead>
				<tbody data-testid={testId}>
					{agents.map((agent) => (
						<AgentRow key={agent.path} agent={agent} jobs={jobs} selected={wanted === agentKey(agent)} selection={selection} />
					))}
				</tbody>
			</table>
		</div>
	);
}

function AgentRow({
	agent,
	jobs,
	selected,
	selection,
}: {
	agent: AgentSummary;
	jobs?: readonly Job[];
	selected: boolean;
	selection?: Selection;
}) {
	const { t: tr } = useTranslation("ufre");
	const { t } = useTranslation("agentsUx");
	const { t: ta } = useTranslation("agentFirst");
	const { t: tf } = useTranslation("uf2a");
	const adapterLabel = useAdapterLabel();
	const key = agentKey(agent);
	const name = agent.id || agent.path.replace(/^.*\//, "").replace(/\.ya?ml$/, "");
	const state = agent.error ? "broken" : jobState(lastJobFor(jobs, "agent.check", agent.path, agent.id));
	const testId = `agents-row-${key.replace(/[^A-Za-z0-9_-]/g, "-")}`;
	const { i18n } = useTranslation();
	const lastRun = agent.error ? null : formatRelative(agent.last_run_at ?? null, i18n.language);
	const runs =
		agent.sealed_runs !== null && !agent.error ? (
			<span data-testid={`${testId}-runs`}>
				{(agent.runs_total ?? 0) > agent.sealed_runs
					? `${t("list.scoredRuns", { count: agent.sealed_runs })} ${t("list.startedRuns", { count: agent.runs_total ?? 0 })}`
					: t("list.scoredRuns", { count: agent.sealed_runs })}
			</span>
		) : null;
	const versions = agent.error ? null : agentVersions(agent);
	const score = agent.error ? null : agentLastScore(agent);
	const model = agent.model ? (
		<span className="truncate font-mono text-xs" title={agent.model}>
			{agent.model}
		</span>
	) : (
		!agent.error && <span className="text-muted-foreground text-xs">{t("list.noModel")}</span>
	);
	return (
		<tr
			className={cn("hover:bg-accent/40 relative border-b transition-colors last:border-b-0", selected && "bg-primary/5")}
			aria-selected={selected || undefined}
			data-state={selected ? "selected" : undefined}
			data-testid={testId}
		>
			{selection && (
				<td className="hidden py-2 pl-4 md:table-cell">
					{selection.deletable.has(agent.id) && (
						<input
							type="checkbox"
							className="accent-primary relative z-10 size-4 cursor-pointer"
							checked={selection.picked.has(agent.id)}
							onChange={(e) => selection.toggle(agent.id, e.target.checked)}
							aria-label={tr("agents.selectAgent", { name })}
							data-testid={`${testId}-select`}
						/>
					)}
				</td>
			)}
			<td className="px-4 py-2 md:h-10 md:w-[22%] md:max-w-0">
				<div className="flex min-w-0 items-center gap-2.5">
					<AgentAvatar name={name} className="size-7 rounded-lg text-xs" />
					<div className="flex min-w-0 flex-col">
						<Link
							to="/agents/$agentId"
							params={{ agentId: key }}
							className="text-foreground truncate font-medium after:absolute after:inset-0 focus-visible:underline focus-visible:outline-none"
							aria-label={t("actions.open", { name })}
							title={name}
						>
							{name}
						</Link>
						{/* Phones: the second line holds model and last run. */}
						<span className="text-muted-foreground flex min-w-0 items-center gap-1.5 truncate text-xs md:hidden">
							{model}
							{lastRun && <span>· {t("list.lastRun", { when: lastRun })}</span>}
						</span>
					</div>
				</div>
			</td>
			<td className="text-muted-foreground hidden px-4 py-2 text-xs md:table-cell">{tf(`agents.type.${adapterKey(agent.adapter) as "command"}`)}</td>
			<td className="hidden max-w-0 min-w-32 px-4 py-2 md:table-cell">
				<span className="flex min-w-0" title={adapterLabel(agent.adapter)}>
					{model}
				</span>
			</td>
			<td className="text-muted-foreground hidden px-4 py-2 text-xs tabular-nums lg:table-cell" data-testid={`${testId}-versions`}>
				{versions !== null && tr("agents.versions", { count: versions })}
			</td>
			<td className="text-muted-foreground hidden px-4 py-2 text-xs tabular-nums lg:table-cell">{runs}</td>
			<td className="hidden px-4 py-2 text-xs tabular-nums lg:table-cell" data-testid={`${testId}-score`}>
				{score && (
					<span title={tr("agents.lastScoreTitle", { passed: Math.round(score.score * score.runs), runs: score.runs })}>
						{formatLastScore(score)}
					</span>
				)}
			</td>
			<td className="text-muted-foreground hidden px-4 py-2 text-xs whitespace-nowrap lg:table-cell">
				{lastRun ? (
					<span data-testid={`${testId}-last`}>{t("list.lastRun", { when: lastRun })}</span>
				) : (
					!agent.error && <span data-testid={`${testId}-last`}>{tr("agents.neverRun")}</span>
				)}
			</td>
			<td className="w-10 px-1 py-2 text-center md:w-auto md:px-4 md:text-left">
				<CheckPill state={state} runs={agentRunsTotal(agent)} testId={`${testId}-status`} compact />
			</td>
			<td className="w-22 px-2 py-2 text-right md:w-auto md:px-4">
				<div className="relative z-10 flex items-center justify-end gap-1">
					{!agent.error && (
						<Button asChild size="sm" variant="ghost" className="h-8">
							<Link to={"/evaluate" as never} search={{ agent: key } as never} data-testid={`${testId}-run`}>
								<Gauge aria-hidden="true" />
								<span className="hidden sm:inline">{ta("list.evaluate")}</span>
							</Link>
						</Button>
					)}
					<RowMenu agent={agent} name={name} testId={testId} />
				</div>
			</td>
		</tr>
	);
}

/** The "..." menu of a row (M2-23): Delete lives here, away from Evaluate, and still asks first. */
function RowMenu({ agent, name, testId }: { agent: AgentSummary; name: string; testId: string }) {
	const { t: tf } = useTranslation("uf2a");
	if (agent.error || !agent.id || isBaselineAgent(agent)) return null;
	return (
		<details className="relative" data-testid={`${testId}-menu`}>
			<summary
				className="hover:bg-accent text-muted-foreground flex size-8 cursor-pointer list-none items-center justify-center rounded-md"
				aria-label={tf("agents.more", { name })}
				title={tf("agents.more", { name })}
			>
				<MoreHorizontal className="size-4" aria-hidden="true" />
			</summary>
			<div className="bg-popover absolute right-0 z-20 mt-1 min-w-36 rounded-md border p-1 shadow-md">
				<AgentRowDelete agent={agent} name={name} testId={testId} />
			</div>
		</details>
	);
}

/** "Delete selected" (Q-122): one confirm (it opens on Cancel), then each picked agent's settings file is deleted. */
function BulkDelete({ ids, onDone }: { ids: readonly string[]; onDone(left: string[]): void }) {
	const { t } = useTranslation("ufre");
	const confirm = useConfirm();
	const [remove] = useDeleteAgentMutation();
	const [busy, setBusy] = useState(false);
	const [failed, setFailed] = useState<string[]>([]);
	const run = async () => {
		const ok = await confirm(t("agents.deleteSelectedBody", { names: ids.join(", ") }), {
			title: t("agents.deleteSelectedTitle", { count: ids.length }),
			confirmLabel: t("agents.deleteSelectedConfirm"),
			cancelLabel: t("agents.deleteSelectedCancel"),
			destructive: true,
		});
		if (!ok) return;
		setBusy(true);
		const bad: string[] = [];
		for (const id of ids) {
			await remove(id)
				.unwrap()
				.catch(() => bad.push(id));
		}
		setBusy(false);
		setFailed(bad);
		// The ones the server refused stay picked, so the error below stays in view.
		onDone(bad);
	};
	return (
		<div className="flex items-center gap-2">
			<Button size="sm" variant="outline" isLoading={busy} dataTestId="agents-delete-selected" onClick={() => void run()}>
				<Trash2 aria-hidden="true" />
				{t("agents.deleteSelected", { count: ids.length })}
			</Button>
			{failed.length > 0 && (
				<p role="alert" className="text-destructive text-xs" data-testid="agents-delete-selected-error">
					{t("agents.deleteSelectedFailed", { names: failed.join(", ") })}
				</p>
			)}
		</div>
	);
}
