import { useTranslation } from "react-i18next";
import { Skeleton } from "@/components/ui/skeleton";
import "@/components/home/ufw1I18n";
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
			className={cn("bg-muted text-foreground flex size-8 shrink-0 items-center justify-center rounded-lg", className)}
			data-track={track ?? "unknown"}
		>
			<Icon className="size-4" aria-hidden="true" />
		</span>
	);
}

export type PillTone = "good" | "warn" | "bad" | "info" | "neutral";

const PILL_CLASS: Record<PillTone, string> = {
	good: "bg-success-soft text-success-ink",
	warn: "bg-warning-soft text-warning-ink",
	bad: "bg-danger-soft text-danger-ink",
	info: "bg-info-soft text-info-ink",
	neutral: "bg-muted text-muted-foreground",
};

const DOT_CLASS: Record<PillTone, string> = {
	good: "bg-success",
	warn: "bg-warning",
	bad: "bg-danger",
	info: "bg-info",
	neutral: "bg-unmeasured",
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
			<span className="bg-info absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:animate-none" />
			<span className="bg-info relative inline-flex size-2 rounded-full" />
		</span>
	);
}

/** One KPI: an icon, a label, a large number and up to two muted lines under it (M2-15); the hint may be a node, such as an error with Retry. */
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
				<span className="line-clamp-2">{label}</span>
				<span className="bg-muted flex size-7 shrink-0 items-center justify-center rounded-lg">
					<Icon className="size-3.5" aria-hidden="true" />
				</span>
			</div>
			{loading ? (
				<StatLoading testId={testId} />
			) : (
				<div className="flex min-w-0 flex-col gap-1">
					<span className="text-foreground truncate text-2xl font-semibold tracking-tight tabular-nums" data-testid={`${testId}-value`}>
						{value}
					</span>
					{hint && <span className="text-muted-foreground line-clamp-2 text-xs">{hint}</span>}
				</div>
			)}
		</div>
	);
}

/** A stat tile's body while its numbers load: the shared skeleton bar and a visible "Loading..." line. */
function StatLoading({ testId }: { testId: string }) {
	const { t } = useTranslation("ufw1");
	return (
		<div className="flex flex-col gap-2" aria-busy="true" data-testid={`${testId}-loading`}>
			<Skeleton className="h-7 w-20" />
			<span className="text-muted-foreground text-xs" aria-live="polite">
				{t("home.loading")}
			</span>
		</div>
	);
}
