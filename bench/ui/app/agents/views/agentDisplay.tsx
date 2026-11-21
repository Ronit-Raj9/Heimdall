import type { AgentSummary, Job } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Bot, Code2, FlaskConical, Globe, Network, SquareTerminal, type LucideIcon } from "lucide-react";
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

export function isTestAgent(agent: TestAgentFields): boolean {
	if (agent.scripted_kind || agent.adapter === "scripted") return true;
	if (TEST_IDS.has(agent.id) || agent.id.startsWith("live-")) return true;
	return TEST_DOUBLE_TEXT.test(`${agent.description ?? ""} ${agent.notes ?? ""}`);
}

const KNOWN_ADAPTERS = ["command", "remote_http", "a2a", "scripted"] as const;

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

const TONES = [
	"bg-sky-500/15 text-sky-700 dark:text-sky-300",
	"bg-violet-500/15 text-violet-700 dark:text-violet-300",
	"bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
	"bg-amber-500/15 text-amber-700 dark:text-amber-300",
	"bg-rose-500/15 text-rose-700 dark:text-rose-300",
	"bg-teal-500/15 text-teal-700 dark:text-teal-300",
];

function toneFor(name: string): string {
	let h = 0;
	for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
	return TONES[h % TONES.length];
}

/** A rounded tile with the agent's initials (or a bot icon when there is no name). */
export function AgentAvatar({ name, size = "md", className }: { name: string; size?: "md" | "lg"; className?: string }) {
	return (
		<span
			aria-hidden="true"
			className={cn(
				"inline-flex shrink-0 items-center justify-center rounded-xl font-semibold tracking-tight",
				size === "lg" ? "size-12 text-base" : "size-10 text-sm",
				toneFor(name),
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

const PILL: Record<CheckState, { dot: string; cls: string }> = {
	passed: { dot: "bg-emerald-500", cls: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300" },
	failed: { dot: "bg-red-500", cls: "bg-red-500/10 text-red-700 dark:text-red-300" },
	running: { dot: "bg-sky-500 animate-pulse", cls: "bg-sky-500/10 text-sky-700 dark:text-sky-300" },
	never: { dot: "bg-muted-foreground/50", cls: "bg-muted text-muted-foreground" },
	broken: { dot: "bg-red-500", cls: "bg-red-500/10 text-red-700 dark:text-red-300" },
};

export function CheckPill({ state, testId }: { state: CheckState; testId?: string }) {
	const { t } = useTranslation("agentsUx");
	const p = PILL[state];
	return (
		<span
			className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap", p.cls)}
			data-state={state}
			data-testid={testId}
		>
			<span className={cn("size-1.5 rounded-full", p.dot)} aria-hidden="true" />
			{t(`status.${state}`)}
		</span>
	);
}
