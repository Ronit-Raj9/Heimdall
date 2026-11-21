import { suiteTrack } from "@/components/scenario/scenario.utils";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import {
	Brain,
	Code2,
	FileText,
	Globe,
	type LucideIcon,
	MessagesSquare,
	Mic,
	Monitor,
	Search,
	ShieldCheck,
	Sparkles,
	SquareTerminal,
	Wrench,
} from "lucide-react";
import { useTranslation } from "react-i18next";

// Lane U04: the suite's face on cards and the suite page (category icon, readable name, one status).

const TRACK_ICON: Record<string, LucideIcon> = {
	coding: Code2,
	terminal: SquareTerminal,
	tool_use: Wrench,
	mcp: Wrench,
	memory: Brain,
	skills: Sparkles,
	safety: ShieldCheck,
	qa: Search,
	browser: Globe,
	computer_use: Monitor,
	artifact: FileText,
	support: MessagesSquare,
	voice: Mic,
};

const TRACK_TINT: Record<string, string> = {
	coding: "bg-sky-500/10 text-sky-600 dark:text-sky-400",
	terminal: "bg-slate-500/10 text-slate-600 dark:text-slate-300",
	tool_use: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
	mcp: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
	memory: "bg-violet-500/10 text-violet-600 dark:text-violet-400",
	skills: "bg-fuchsia-500/10 text-fuchsia-600 dark:text-fuchsia-400",
	safety: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
	qa: "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400",
	browser: "bg-cyan-500/10 text-cyan-600 dark:text-cyan-400",
	computer_use: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
	artifact: "bg-orange-500/10 text-orange-600 dark:text-orange-400",
	support: "bg-teal-500/10 text-teal-600 dark:text-teal-400",
	voice: "bg-rose-500/10 text-rose-600 dark:text-rose-400",
};

/** The suite's category icon in a soft rounded tile. */
export function SuiteIconTile({ suite, size = "md" }: { suite: Pick<SuiteSummary, "track">; size?: "md" | "lg" }) {
	const track = suiteTrack(suite);
	const Icon = TRACK_ICON[track] ?? Code2;
	return (
		<span
			className={cn(
				"inline-flex shrink-0 items-center justify-center rounded-xl",
				TRACK_TINT[track] ?? TRACK_TINT.coding,
				size === "lg" ? "size-14" : "size-10",
			)}
			data-track={track}
			aria-hidden="true"
		>
			<Icon className={size === "lg" ? "size-7" : "size-5"} />
		</span>
	);
}

/** "coding-core" reads as "Coding core": suites carry no display name, so the id is humanised. */
export function suiteDisplayName(id: string): string {
	const words = id.replace(/[-_]+/g, " ").trim();
	return words ? words.charAt(0).toUpperCase() + words.slice(1) : id;
}

export type SuiteReadiness = "ready" | "needsAdmission" | "needsLock" | "noTasks" | "error";

/** One status for the whole suite: ready means every task admitted and the lock current. */
export function suiteReadiness(suite: Pick<SuiteSummary, "error" | "task_count" | "admitted_count" | "lock_state">): SuiteReadiness {
	if (suite.error) return "error";
	if (suite.task_count === 0) return "noTasks";
	if (suite.admitted_count < suite.task_count) return "needsAdmission";
	return suite.lock_state === "locked" ? "ready" : "needsLock";
}

const PILL_TONE: Record<SuiteReadiness, string> = {
	ready: "bg-chart-success/10 text-chart-success-ink",
	needsAdmission: "bg-chart-warning/10 text-chart-warning-ink",
	needsLock: "bg-chart-warning/10 text-chart-warning-ink",
	noTasks: "bg-muted text-muted-foreground",
	error: "bg-chart-error/10 text-chart-error-ink",
};

const DOT_TONE: Record<SuiteReadiness, string> = {
	ready: "bg-chart-success",
	needsAdmission: "bg-chart-warning",
	needsLock: "bg-chart-warning",
	noTasks: "bg-muted-foreground/60",
	error: "bg-chart-error",
};

/** The suite's single status pill: dot plus a few words. */
export function SuiteStatusPill({ suite, testId }: { suite: SuiteSummary; testId?: string }) {
	const { t } = useTranslation("suitesUx");
	const state = suiteReadiness(suite);
	return (
		<span
			className={cn("inline-flex w-fit items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap", PILL_TONE[state])}
			data-testid={testId}
			data-status={state}
		>
			<span className={cn("size-1.5 rounded-full", DOT_TONE[state])} aria-hidden="true" />
			{t(`status.${state}`, { count: suite.task_count - suite.admitted_count })}
		</span>
	);
}
