import type { TrackGroup } from "@/components/scenario/scenario.utils";
import { cn } from "@/lib/utils";
import {
	Boxes,
	Brain,
	Code2,
	Globe,
	HelpCircle,
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
import { useTranslation } from "react-i18next";

/** The category icon of each track. */
export const TRACK_ICONS: Record<string, LucideIcon> = {
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

export function trackIcon(track: string): LucideIcon {
	return TRACK_ICONS[track] ?? (track.startsWith("~") ? HelpCircle : Boxes);
}

/** Suite filter tabs: one pill per track with its icon; picking a track picks its newest suite. */
export function SuiteTabs({
	groups,
	track,
	onTrack,
	disabled,
}: {
	groups: readonly TrackGroup[];
	track: string | null;
	onTrack: (track: string | null) => void;
	disabled?: boolean;
}) {
	const { t: tu } = useTranslation("u12");
	const { t: tb } = useTranslation("bench");
	if (groups.length < 2) return null;
	const tab = (active: boolean) =>
		cn(
			"inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-sm whitespace-nowrap transition-colors focus-visible:ring-ring/50 focus-visible:ring-2 focus-visible:outline-none disabled:opacity-50",
			active ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted hover:text-foreground",
		);
	return (
		<div role="tablist" aria-label={tu("board.suiteTabs")} className="flex gap-1 overflow-x-auto pb-1" data-testid="leaderboard-suite-tabs">
			<button type="button" role="tab" aria-selected={!track} className={tab(!track)} disabled={disabled} onClick={() => onTrack(null)}>
				<Boxes className="size-4" aria-hidden="true" />
				{tu("board.allSuites")}
			</button>
			{groups.map((g) => {
				const Icon = trackIcon(g.track);
				const active = track === g.track;
				return (
					<button
						key={g.track}
						type="button"
						role="tab"
						aria-selected={active}
						className={tab(active)}
						disabled={disabled}
						onClick={() => onTrack(active ? null : g.track)}
						data-testid={`leaderboard-suite-tab-${g.track}`}
					>
						<Icon className="size-4" aria-hidden="true" />
						{tb(`scenario.track.${g.track}`, { defaultValue: g.track.replace(/^~/, "").replace(/_/g, " ") })}
					</button>
				);
			})}
		</div>
	);
}
