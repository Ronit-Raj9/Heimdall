import type { Track } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import {
	Boxes,
	Brain,
	Code2,
	Globe,
	MessagesSquare,
	Monitor,
	Package,
	Search,
	ShieldCheck,
	Sparkles,
	SquareTerminal,
	Wrench,
	type LucideIcon,
} from "lucide-react";
import type { ReactNode } from "react";

const TRACK_ICON: Record<Track, LucideIcon> = {
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
	artifact: Package,
	support: MessagesSquare,
};

/** The category icon of a suite's track; a suite with no known track gets a neutral box. */
export function trackIcon(track: string | undefined): LucideIcon {
	return (track && TRACK_ICON[track as Track]) || Boxes;
}

/** A suite's category icon in a soft rounded square. */
export function TrackBadge({ track, className }: { track: string | undefined; className?: string }) {
	const Icon = trackIcon(track);
	return (
		<span
			className={cn("bg-muted text-foreground/80 flex size-8 shrink-0 items-center justify-center rounded-lg", className)}
			data-track={track ?? "unknown"}
		>
			<Icon className="size-4" aria-hidden="true" />
		</span>
	);
}

export type PillTone = "good" | "warn" | "bad" | "info" | "neutral";

const PILL_CLASS: Record<PillTone, string> = {
	good: "bg-chart-success/12 text-chart-success-ink",
	warn: "bg-chart-warning/15 text-chart-warning-ink",
	bad: "bg-chart-error/12 text-chart-error-ink",
	info: "bg-chart-info/12 text-chart-info-ink",
	neutral: "bg-muted text-muted-foreground",
};

const DOT_CLASS: Record<PillTone, string> = {
	good: "bg-chart-success",
	warn: "bg-chart-warning",
	bad: "bg-chart-error",
	info: "bg-chart-info",
	neutral: "bg-muted-foreground/60",
};

/** A small rounded status pill: a coloured dot and a word. */
export function Pill({ tone, children, testId, className }: { tone: PillTone; children: ReactNode; testId?: string; className?: string }) {
	return (
		<span
			className={cn(
				"inline-flex shrink-0 items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap",
				PILL_CLASS[tone],
				className,
			)}
			data-testid={testId}
			data-tone={tone}
		>
			<span className={cn("size-1.5 rounded-full", DOT_CLASS[tone])} aria-hidden="true" />
			{children}
		</span>
	);
}

/** A pulsing blue dot for anything live. */
export function LiveDot({ className }: { className?: string }) {
	return (
		<span aria-hidden="true" className={cn("relative flex size-2", className)}>
			<span className="bg-chart-info absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:animate-none" />
			<span className="bg-chart-info relative inline-flex size-2 rounded-full" />
		</span>
	);
}

/** One KPI: an icon, a label, a large number and one muted line under it. */
export function StatCard({
	icon: Icon,
	label,
	value,
	hint,
	loading,
	testId,
}: {
	icon: LucideIcon;
	label: string;
	value: ReactNode;
	hint?: ReactNode;
	loading?: boolean;
	testId: string;
}) {
	return (
		<div
			className="bg-card flex min-w-0 flex-col gap-3 rounded-xl border p-5 shadow-(--shadow-card) transition-shadow hover:shadow-md"
			data-testid={testId}
		>
			<div className="text-muted-foreground flex items-center justify-between gap-2 text-sm">
				<span className="truncate">{label}</span>
				<span className="bg-muted flex size-7 shrink-0 items-center justify-center rounded-lg">
					<Icon className="size-3.5" aria-hidden="true" />
				</span>
			</div>
			{loading ? (
				<div className="flex flex-col gap-2" aria-hidden="true">
					<span className="bg-muted h-7 w-20 animate-pulse rounded-md" />
					<span className="bg-muted h-3 w-28 animate-pulse rounded-md" />
				</div>
			) : (
				<div className="flex min-w-0 flex-col gap-1">
					<span className="text-foreground truncate text-2xl font-semibold tracking-tight tabular-nums" data-testid={`${testId}-value`}>
						{value}
					</span>
					{hint && <span className="text-muted-foreground truncate text-xs">{hint}</span>}
				</div>
			)}
		</div>
	);
}
