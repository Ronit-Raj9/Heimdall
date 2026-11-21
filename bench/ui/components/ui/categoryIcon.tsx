import { cn } from "@/lib/utils";
import {
	Boxes,
	Brain,
	Code2,
	Cpu,
	Globe,
	type LucideIcon,
	MessagesSquare,
	Mic,
	Monitor,
	Search,
	ShieldCheck,
	SquareTerminal,
	Users,
	Wrench,
} from "lucide-react";

interface CategoryStyle {
	icon: LucideIcon;
	/** Soft tile background + icon color. */
	tile: string;
}

const STYLES: Record<string, CategoryStyle> = {
	coding: { icon: Code2, tile: "bg-sky-500/10 text-sky-600 dark:text-sky-400" },
	terminal: { icon: SquareTerminal, tile: "bg-slate-500/10 text-slate-700 dark:text-slate-300" },
	memory: { icon: Brain, tile: "bg-pink-500/10 text-pink-600 dark:text-pink-400" },
	computer_use: { icon: Monitor, tile: "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400" },
	browser: { icon: Globe, tile: "bg-cyan-500/10 text-cyan-700 dark:text-cyan-400" },
	voice: { icon: Mic, tile: "bg-rose-500/10 text-rose-600 dark:text-rose-400" },
	support: { icon: MessagesSquare, tile: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400" },
	tool_use: { icon: Wrench, tile: "bg-amber-500/10 text-amber-700 dark:text-amber-400" },
	research: { icon: Search, tile: "bg-violet-500/10 text-violet-600 dark:text-violet-400" },
	safety: { icon: ShieldCheck, tile: "bg-teal-500/10 text-teal-700 dark:text-teal-400" },
	team: { icon: Users, tile: "bg-orange-500/10 text-orange-700 dark:text-orange-400" },
	gpu: { icon: Cpu, tile: "bg-lime-500/10 text-lime-700 dark:text-lime-400" },
};

const ALIASES: Record<string, string> = {
	code: "coding",
	swe: "coding",
	shell: "terminal",
	cli: "terminal",
	computer: "computer_use",
	desktop: "computer_use",
	cua: "computer_use",
	gui: "computer_use",
	web: "browser",
	audio: "voice",
	speech: "voice",
	chat: "support",
	customer_support: "support",
	conversation: "support",
	mcp: "tool_use",
	tools: "tool_use",
	tool: "tool_use",
	function_calling: "tool_use",
	search: "research",
	deep_research: "research",
	security: "safety",
	alignment: "safety",
	multi_agent: "team",
	multiagent: "team",
	agents: "team",
	cuda: "gpu",
	kernel: "gpu",
	kernels: "gpu",
};

const FALLBACK: CategoryStyle = { icon: Boxes, tile: "bg-muted text-muted-foreground" };

/** Normalizes "Computer Use", "computer-use" and "computer_use" to one key. */
export function normalizeCategory(category: string | null | undefined): string {
	const key = (category ?? "")
		.trim()
		.toLowerCase()
		.replace(/[\s-]+/g, "_");
	return ALIASES[key] ?? key;
}

/** The lucide icon and tile classes for a category string; unknown categories get a neutral box. */
export function categoryStyle(category: string | null | undefined): CategoryStyle {
	return STYLES[normalizeCategory(category)] ?? FALLBACK;
}

const SIZES = {
	sm: { tile: "size-7 rounded-md", icon: "size-3.5" },
	md: { tile: "size-9 rounded-lg", icon: "size-4" },
	lg: { tile: "size-11 rounded-xl", icon: "size-5" },
} as const;

export interface CategoryIconProps {
	category: string | null | undefined;
	size?: keyof typeof SIZES;
	/** Accessible name; when omitted the tile is decorative. */
	label?: string;
	/** Bare icon without the tile. */
	bare?: boolean;
	className?: string;
	testId?: string;
}

/** A category icon in a soft colored tile (coding, terminal, memory, browser, gpu, ...). */
export function CategoryIcon({ category, size = "md", label, bare = false, className, testId }: CategoryIconProps) {
	const { icon: Icon, tile } = categoryStyle(category);
	const s = SIZES[size];
	const a11y = label ? { role: "img", "aria-label": label, title: label } : { "aria-hidden": true as const };
	if (bare) {
		return (
			<span
				data-slot="category-icon"
				data-category={normalizeCategory(category)}
				data-testid={testId}
				className={cn("inline-flex", className)}
				{...a11y}
			>
				<Icon className={s.icon} />
			</span>
		);
	}
	return (
		<span
			data-slot="category-icon"
			data-category={normalizeCategory(category)}
			data-testid={testId}
			className={cn("inline-flex shrink-0 items-center justify-center", s.tile, tile, className)}
			{...a11y}
		>
			<Icon className={s.icon} />
		</span>
	);
}