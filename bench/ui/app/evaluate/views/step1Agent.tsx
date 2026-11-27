import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { BASELINE_AGENTS, useGetAgentVersionsQuery, type Capability } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/evaluateI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Bot, Check, Plug, Plus } from "lucide-react";
import { useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { agentPickPatch, useEvaluateDraft } from "./evaluateDraft";
import { agentCapabilities, latestVersion, REGISTER_RETURN, splitAgents, type AgentWithCaps } from "./evaluate.utils";
import "./gw09I18n";

export function CapabilityChips({ caps }: { caps: Capability[] }) {
	const { t } = useTranslation("evaluate");
	return (
		<span className="flex flex-wrap gap-1">
			{caps.map((c) => (
				<Badge key={c} variant="neutral" data-testid={`cap-${c}`}>
					{t(`capability.${c}`, { defaultValue: c })}
				</Badge>
			))}
		</span>
	);
}

/**
 * One agent as a compact radio row (M3-39): name, default model, capability chips. Rows of one list form a radio group with
 * one tab stop; arrow keys move between them (see agentListKeys).
 */
function AgentRow({
	agent,
	selected,
	tabbable,
	onPick,
}: {
	agent: AgentWithCaps;
	selected: boolean;
	tabbable: boolean;
	onPick(): void;
}) {
	const { t } = useTranslation("evaluate");
	return (
		<button
			type="button"
			role="radio"
			aria-checked={selected}
			tabIndex={tabbable ? 0 : -1}
			onClick={onPick}
			data-testid={`evaluate-agent-${agent.id}`}
			data-agent-row
			className={cn(
				"grid min-h-11 w-full grid-cols-[1fr_auto] items-center gap-3 border-b px-3 py-1.5 text-left last:border-b-0 sm:grid-cols-[minmax(0,1fr)_minmax(0,14rem)_auto]",
				"hover:bg-muted focus-visible:ring-ring cursor-pointer outline-none focus-visible:ring-2 focus-visible:ring-inset",
				selected && "bg-primary/5",
			)}
		>
			<span className="flex min-w-0 items-center gap-2">
				{selected ? <Check className="text-primary size-4 shrink-0" aria-hidden /> : <span className="size-4 shrink-0" aria-hidden />}
				<span className="flex min-w-0 flex-col">
					<span className="truncate text-sm font-medium">{agent.id}</span>
					{agent.model && (
						<span className="text-muted-foreground truncate text-xs" title={t("agent.defaultModel")}>
							{agent.model}
						</span>
					)}
				</span>
				{BASELINE_AGENTS.includes(agent.id) && (
					<Badge variant="info" className="shrink-0">
						{t("agent.baseline")}
					</Badge>
				)}
			</span>
			<span className="hidden min-w-0 sm:block">
				<CapabilityChips caps={agentCapabilities(agent)} />
			</span>
			<Bot className="text-muted-foreground size-4" aria-hidden />
		</button>
	);
}

/** Arrow keys inside a radio group of agent rows move focus to the next or previous row (M3-39). */
export function agentListKeys(e: KeyboardEvent<HTMLElement>): void {
	if (!["ArrowDown", "ArrowUp", "ArrowRight", "ArrowLeft", "Home", "End"].includes(e.key)) return;
	const rows = Array.from(e.currentTarget.querySelectorAll<HTMLElement>("[data-agent-row]"));
	const i = rows.indexOf(document.activeElement as HTMLElement);
	if (rows.length === 0) return;
	e.preventDefault();
	const next =
		e.key === "Home" ? 0 : e.key === "End" ? rows.length - 1 : e.key === "ArrowDown" || e.key === "ArrowRight" ? (i + 1) % rows.length : (i - 1 + rows.length) % rows.length;
	rows[next]?.focus();
}

/** Agents whose id or model contains the query, case blind; all when the query is blank. */
export function filterAgents(list: readonly AgentWithCaps[], query: string): AgentWithCaps[] {
	const q = query.trim().toLowerCase();
	if (!q) return [...list];
	return list.filter((a) => a.id.toLowerCase().includes(q) || (a.model ?? "").toLowerCase().includes(q));
}

/** Version picker for the chosen agent, from GET /agents/{id}/versions. */
export function VersionPicker({
	agent,
	value,
	onChange,
	label,
	testId,
	disabledVersion,
	disabledSuffix,
	countHint,
}: {
	agent: string;
	value: number | null;
	onChange(v: number | null): void;
	label?: string;
	testId?: string;
	/** A version that cannot be picked (step 2: Setup A's), shown with `disabledSuffix`. */
	disabledVersion?: number | null;
	disabledSuffix?: string;
	/** Say how many versions there are and which is the latest (step 1). */
	countHint?: boolean;
}) {
	const { t } = useTranslation("evaluate");
	const { t: tg } = useTranslation("gw09");
	const q = useGetAgentVersionsQuery(agent);
	const versions = [...(q.data?.versions ?? [])].sort((a, b) => b.version - a.version);
	const latest = latestVersion(versions);
	return (
		<label className="flex flex-col gap-1.5 text-sm">
			<span className="font-medium">{label ?? t("agent.version")}</span>
			<select
				className="border-input bg-background h-9 w-56 rounded-lg border px-2 text-sm"
				value={value == null ? "" : String(value)}
				onChange={(e) => onChange(e.target.value === "" ? null : Number(e.target.value))}
				data-testid={testId ?? "evaluate-version"}
				disabled={q.isLoading}
			>
				<option value="">{t("agent.latest")}</option>
				{versions.map((v) => (
					<option key={v.version} value={v.version} disabled={disabledVersion === v.version}>
						{v.runs != null ? t("agent.versionRuns", { n: v.version, runs: v.runs }) : t("agent.versionOption", { n: v.version })}
						{disabledVersion === v.version ? disabledSuffix : ""}
					</option>
				))}
			</select>
			{!q.isLoading && versions.length === 0 && <span className="text-muted-foreground text-xs">{t("agent.noVersions")}</span>}
			{countHint && versions.length >= 2 && latest !== undefined && (
				<span className="text-muted-foreground text-xs" data-testid="evaluate-versions-hint">
					{tg("agent.versionsHint", { n: versions.length, max: latest })}
				</span>
			)}
		</label>
	);
}

/**
 * Step 1: pick the agent under test (org or built-in), or register one, and its version. `missingAgent` is an agent
 * a link named that this organization does not have (N86); `onPicked` runs on every card click.
 */
export function Step1Agent({ missingAgent, onPicked }: { missingAgent?: string | null; onPicked?(): void } = {}) {
	const { t } = useTranslation("evaluate");
	const { t: tg } = useTranslation("gw09");
	const { draft, update } = useEvaluateDraft();
	const q = useGetAgentsQuery();
	const all = (q.data?.agents ?? []) as AgentWithCaps[];
	const { own, builtin } = splitAgents(all);

	const pick = (a: AgentWithCaps) => {
		onPicked?.();
		if (a.id === draft.agent) return;
		update(agentPickPatch(draft, a));
	};

	const [query, setQuery] = useState("");
	const ownShown = filterAgents(own, query);
	const builtinShown = filterAgents(builtin, query);
	const grid = (list: AgentWithCaps[], label: string) => {
		const selectedIn = list.some((a) => a.id === draft.agent);
		return (
			<div role="radiogroup" aria-label={label} className="flex flex-col rounded-lg border" onKeyDown={agentListKeys}>
				{list.map((a, i) => (
					<AgentRow
						key={a.id}
						agent={a}
						selected={draft.agent === a.id}
						tabbable={selectedIn ? draft.agent === a.id : i === 0}
						onPick={() => pick(a)}
					/>
				))}
			</div>
		);
	};

	return (
		<div className="flex flex-col gap-6" data-testid="evaluate-step-1">
			<div className="flex flex-col gap-1">
				<h2 className="text-lg font-semibold">{t("agent.heading")}</h2>
				<p className="text-muted-foreground text-sm">{t("agent.hint")}</p>
			</div>

			{missingAgent && (
				<div
					role="status"
					className="border-chart-warning/40 bg-chart-warning/10 flex flex-wrap items-center gap-3 rounded-lg border px-3 py-2 text-sm"
					data-testid="evaluate-agent-missing"
				>
					<span>{tg("agent.notFound", { id: missingAgent })}</span>
					<Button asChild variant="outline" size="sm">
						<Link to="/agents/new" search={{ return: REGISTER_RETURN } as never} data-testid="evaluate-agent-missing-register">
							<Plus />
							{t("agent.register")}
						</Link>
					</Button>
				</div>
			)}

			{q.isLoading ? (
				<p className="text-muted-foreground text-sm" data-testid="evaluate-agents-loading">
					{t("agent.loading")}
				</p>
			) : q.isError ? (
				<div className="flex items-center gap-3 text-sm">
					<span className="text-destructive">{t("agent.loadError")}</span>
					<Button variant="outline" size="sm" onClick={() => void q.refetch()}>
						{t("agent.retry")}
					</Button>
				</div>
			) : (
				<>
					{all.length > 6 && (
						<input
							type="search"
							value={query}
							onChange={(e) => setQuery(e.target.value)}
							placeholder={t("agent.search")}
							aria-label={t("agent.search")}
							className="border-input bg-background h-10 w-full max-w-md rounded-md border px-3 text-sm"
							data-testid="evaluate-agent-search"
						/>
					)}
					<section className="flex flex-col gap-3">
						<div className="flex flex-wrap items-center justify-between gap-2">
							<h3 className="text-sm font-semibold">{t("agent.yours")}</h3>
							{own.length > 0 && (
								<Button asChild variant="outline" size="sm">
									<Link to="/agents/new" search={{ return: REGISTER_RETURN } as never} data-testid="evaluate-register">
										<Plus />
										{t("agent.register")}
									</Link>
								</Button>
							)}
						</div>
						{draft.agent && (
							<div
								className="border-primary/30 bg-primary/5 flex flex-wrap items-end gap-4 rounded-xl border p-3"
								data-testid="evaluate-selected-agent"
							>
								<span className="flex items-center gap-2 pb-2 text-sm font-medium">
									<Check className="text-primary size-4" aria-hidden />
									{tg("agent.selected", { agent: draft.agent })}
								</span>
								<VersionPicker
									agent={draft.agent}
									value={draft.version}
									onChange={(v) => update({ version: v })}
									label={tg("agent.versionOf", { agent: draft.agent })}
									countHint
								/>
							</div>
						)}
						{own.length === 0 ? (
							// The main path for a new user is a full-width row, not a small button (X-26).
							<div className="flex flex-col gap-3 rounded-lg border border-dashed p-4 sm:flex-row sm:items-center" data-testid="evaluate-register-row">
								<span className="flex min-w-0 flex-1 flex-col gap-0.5">
									<span className="text-sm font-medium">{t("agentList.registerRow")}</span>
									<span className="text-muted-foreground text-sm">{t("agent.registerHint")}</span>
								</span>
								<Button asChild className="w-full sm:w-auto">
									<Link to="/agents/new" search={{ return: REGISTER_RETURN } as never} data-testid="evaluate-register">
										<Plus />
										{t("agent.register")}
									</Link>
								</Button>
							</div>
						) : ownShown.length ? (
							grid(ownShown, t("agent.yours"))
						) : (
							<p className="text-muted-foreground text-sm">{t("agentList.noMatch", { query })}</p>
						)}
					</section>
					<section className="flex flex-col gap-3">
						<h3 className="text-sm font-semibold">{t("agent.builtin")}</h3>
						{builtinShown.length ? (
							grid(builtinShown, t("agent.builtin"))
						) : (
							<p className="text-muted-foreground text-sm">
								{builtin.length && query ? t("agentList.noMatch", { query }) : t("agentList.noBuiltIn")}
							</p>
						)}
					</section>
				</>
			)}

			{/* A plain row, not a boxed card, so the text keeps its width on phones (M3-41). */}
			<section className="flex flex-col gap-3 border-t pt-4 sm:flex-row sm:items-center" data-testid="evaluate-mcp-server">
				<span className="bg-muted text-muted-foreground hidden size-9 shrink-0 items-center justify-center rounded-lg sm:flex">
					<Plug className="size-4" aria-hidden />
				</span>
				<span className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="text-sm font-semibold">{t("agent.mcpServer.title")}</span>
					<span className="text-muted-foreground text-sm">{t("agent.mcpServer.hint")}</span>
				</span>
				<Button asChild variant="outline" size="sm" className="w-full sm:w-auto">
					<Link to={"/mcp-test" as never} data-testid="evaluate-mcp-server-link">
						{t("agent.mcpServer.cta")}
					</Link>
				</Button>
			</section>

			{!draft.agent && <p className="text-muted-foreground text-sm">{t("agent.pickFirst")}</p>}
		</div>
	);
}

export default Step1Agent;