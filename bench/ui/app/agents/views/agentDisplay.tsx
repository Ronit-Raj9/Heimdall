import type { AgentSummary, Job } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Bot, Code2, FlaskConical, Globe, Network, PlugZap, SquareTerminal, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * Scripted controls, the test doubles the example benchmarks ship (voice-broken, unsafe, storm, ...)
 * and leftover live-check agents are grouped apart from real agents.
 */
const TEST_IDS = new Set([
	"oracle",
	"noop",
	"toy",
	"broken",
	"exploit",
	"refuse",
	"voice-broken",
	"unsafe",
	"storm",
	"a2a-broken",
	"codex-task",
]);

/** A description or notes line that calls the agent a test double, stub, fake or demo agent. */
const TEST_DOUBLE_TEXT = /\b(test[ -]?double|stub agent|fake agent|mock agent|demo agent|for (the )?example benchmarks?)\b/i;

type TestAgentFields = Pick<AgentSummary, "id" | "scripted_kind" | "adapter"> & { description?: string; notes?: string };

/** Agents every new org starts with, shown for comparison under "Baseline agents". */
export const BASELINE_AGENT_IDS: ReadonlySet<string> = new Set([
	"codex",
	"claude-code",
	"mini-swe-agent",
	"opencode",
	"gemini-cli",
	"fw-plain-openai",
	"custom-python",
]);

export function isBaselineAgent(agent: Pick<AgentSummary, "id">): boolean {
	return BASELINE_AGENT_IDS.has(agent.id);
}

export function isTestAgent(agent: TestAgentFields): boolean {
	if (agent.scripted_kind || agent.adapter === "scripted") return true;
	if (TEST_IDS.has(agent.id) || agent.id.startsWith("live-") || agent.id.startsWith("zz-test-")) return true;
	return TEST_DOUBLE_TEXT.test(`${agent.description ?? ""} ${agent.notes ?? ""}`);
}

/** How the agents list can be ordered (URL state `sort`). */
export const AGENT_SORTS = ["name", "last", "runs", "score"] as const;
export type AgentSort = (typeof AGENT_SORTS)[number];

/** The newest scored evaluation of an agent as GET /agents serves it (optional: older servers omit it). */
export interface AgentLastScore {
	/** Share of passing runs, 0..1. */
	score: number;
	runs: number;
	exp_id: string;
	at: string;
}

/** The fields GET /agents adds per agent (round 2, Q-123); read as optional so an older server still works. */
export type AgentScoreFields = { versions?: number | null; last_score?: AgentLastScore | null };

/** The last score of an agent, or null when the server sent none. */
export function agentLastScore(agent: object): AgentLastScore | null {
	const s = (agent as AgentScoreFields).last_score;
	return s && typeof s.score === "number" && Number.isFinite(s.score) ? s : null;
}

/** How many config versions the agent ran with, or null when the server sent none. */
export function agentVersions(agent: object): number | null {
	const v = (agent as AgentScoreFields).versions;
	return typeof v === "number" && v > 0 ? v : null;
}

/** A last score as a whole percent ("40%"). */
export function formatLastScore(s: AgentLastScore): string {
	return `${Math.round(s.score * 100)}%`;
}

/** The agents whose id, file, model or adapter contains q (case-insensitive). An empty q keeps every agent. */
export function filterAgents<T extends Pick<AgentSummary, "id" | "path" | "model" | "adapter">>(
	agents: readonly T[],
	q: string | null | undefined,
): T[] {
	const needle = (q ?? "").trim().toLowerCase();
	if (!needle) return [...agents];
	return agents.filter((agent) =>
		[agent.id, agent.path, agent.model, agent.adapter].some((field) => (field ?? "").toLowerCase().includes(needle)),
	);
}

/** Every run of the agent the server knows of, any outcome; the scored count when the total is not served. */
export function agentRunsTotal(agent: Pick<AgentSummary, "sealed_runs" | "runs_total">): number {
	return agent.runs_total ?? agent.sealed_runs ?? 0;
}

function lastRunMs(agent: Pick<AgentSummary, "last_run_at">): number {
	const ms = agent.last_run_at ? Date.parse(agent.last_run_at) : Number.NaN;
	return Number.isNaN(ms) ? Number.NEGATIVE_INFINITY : ms;
}

function nameOf(agent: Pick<AgentSummary, "id" | "path">): string {
	return agent.id || agent.path;
}

/**
 * The agents in the chosen order: by name (A to Z), by last run (newest first, never-run last) or
 * by runs (most first) or by last score (highest first, unscored last). Ties fall back to the name, so the order is stable.
 */
export function sortAgents<T extends Pick<AgentSummary, "id" | "path" | "sealed_runs" | "runs_total" | "last_run_at">>(
	agents: readonly T[],
	sort: AgentSort,
): T[] {
	const byName = (a: T, b: T) => nameOf(a).localeCompare(nameOf(b));
	const out = [...agents];
	if (sort === "last") return out.sort((a, b) => lastRunMs(b) - lastRunMs(a) || byName(a, b));
	if (sort === "score") {
		const score = (a: T) => agentLastScore(a)?.score ?? Number.NEGATIVE_INFINITY;
		return out.sort((a, b) => score(b) - score(a) || byName(a, b));
	}
	if (sort === "runs")
		return out.sort((a, b) => agentRunsTotal(b) - agentRunsTotal(a) || (b.sealed_runs ?? 0) - (a.sealed_runs ?? 0) || byName(a, b));
	return out.sort(byName);
}

const KNOWN_ADAPTERS = ["command", "remote_http", "a2a", "pull", "scripted"] as const;

/** The adapter key the agentsUx catalog labels ("CLI agent", "HTTP endpoint"). */
export function adapterKey(adapter: string): (typeof KNOWN_ADAPTERS)[number] | "other" {
	return (KNOWN_ADAPTERS as readonly string[]).includes(adapter) ? (adapter as (typeof KNOWN_ADAPTERS)[number]) : "other";
}

export function useAdapterLabel(): (adapter: string) => string {
	const { t } = useTranslation("agentsUx");
	return (adapter) => t(`adapter.${adapterKey(adapter)}`);
}

export function adapterIcon(adapter: string): LucideIcon {
	switch (adapterKey(adapter)) {
		case "command":
			return SquareTerminal;
		case "remote_http":
			return Globe;
		case "a2a":
			return Network;
		case "pull":
			return PlugZap;
		case "scripted":
			return FlaskConical;
		default:
			return Code2;
	}
}

/** Two letters from an id like "claude-code" (CC) or "codex" (CO). */
export function initials(name: string): string {
	const parts = name.split(/[-_.\s]+/).filter(Boolean);
	if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
	return (name.slice(0, 2) || "?").toUpperCase();
}

// M2-29: one neutral tile for every agent; the initials tell agents apart, so no tint reads as a status.
const AVATAR_TONE = "bg-muted text-foreground";

/** A rounded tile with the agent's initials (or a bot icon when there is no name). */
export function AgentAvatar({ name, size = "md", className }: { name: string; size?: "md" | "lg"; className?: string }) {
	return (
		<span
			aria-hidden="true"
			className={cn(
				"inline-flex shrink-0 items-center justify-center rounded-xl font-semibold tracking-tight",
				size === "lg" ? "size-12 text-base" : "size-10 text-sm",
				AVATAR_TONE,
				className,
			)}
		>
			{name ? initials(name) : <Bot className="size-5" />}
		</span>
	);
}

export type CheckState = "passed" | "failed" | "running" | "never" | "broken";

/** The newest job of kind for the agent at path (jobs are newest first). */
export function lastJobFor(jobs: readonly Job[] | undefined, kind: string, agentPath: string, agentId?: string): Job | undefined {
	return jobs?.find((j) => j.kind === kind && (j.params?.agent === agentPath || (agentId !== undefined && j.params?.agent === agentId)));
}

export function jobState(job: Job | undefined): CheckState {
	if (!job) return "never";
	if (job.status === "succeeded") return "passed";
	if (job.status === "queued" || job.status === "running") return "running";
	return "failed";
}

// M2-112f: the shared status tokens of globals.css (lane S1).
const PILL: Record<CheckState, { dot: string; cls: string }> = {
	passed: { dot: "bg-success", cls: "bg-success-soft text-success-ink" },
	failed: { dot: "bg-danger", cls: "bg-danger-soft text-danger-ink" },
	running: { dot: "bg-info animate-pulse", cls: "bg-info-soft text-info-ink" },
	never: { dot: "bg-unmeasured", cls: "bg-muted text-muted-foreground" },
	broken: { dot: "bg-danger", cls: "bg-danger-soft text-danger-ink" },
};

/**
 * The setup check state. "Setup not checked" is left out for an agent that already has runs
 * (pass `runs`), since the runs show it works (M2-22).
 */
export function CheckPill({ state, testId, runs, compact }: { state: CheckState; testId?: string; runs?: number; compact?: boolean }) {
	const { t } = useTranslation("agentsUx");
	if (state === "never" && (runs ?? 0) > 0) return null;
	const p = PILL[state];
	return (
		<span
			className={cn("inline-flex items-center gap-1.5 rounded-full py-0.5 text-xs font-medium whitespace-nowrap", compact ? "px-1.5 md:px-2" : "px-2", p.cls)}
			data-state={state}
			data-testid={testId}
		>
			<span className={cn("size-1.5 rounded-full", p.dot)} aria-hidden="true" />
			{/* compact: phones show the dot only, so the name keeps the row width (Q-2); the text stays for screen readers. */}
			<span className={compact ? "sr-only md:not-sr-only" : undefined}>{t(`status.${state}`)}</span>
		</span>
	);
}