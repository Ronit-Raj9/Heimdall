// Small building blocks for live experiment views: state pill, slot bar, stage chip, slot row and
// the live event ticker. Shared by ExperimentProgressCard and LiveRunCard.
import { ValueCell } from "@/components/valueCell";
import type { LiveSlotView } from "@/lib/hooks/useLiveExperiment";
import type { ExperimentStatus, LiveEvent } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Ban, Check, CircleAlert, Clock, Loader2, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { formatElapsed } from "./liveExperiment.reducer";

type Tone = "live" | "good" | "bad" | "muted";

const TONE_PILL: Record<Tone, string> = {
	live: "bg-chart-info/10 text-chart-info-ink",
	good: "bg-chart-success/10 text-chart-success-ink",
	bad: "bg-chart-error/10 text-chart-error-ink",
	muted: "bg-muted text-muted-foreground",
};

const STATE_TONE: Record<ExperimentStatus["state"], Tone> = {
	created: "muted",
	running: "live",
	analyzing: "live",
	done: "good",
	failed: "bad",
	cancelled: "muted",
};

/** A pulsing dot for live things, a solid one otherwise. */
export function LiveDot({ tone, className }: { tone: Tone; className?: string }) {
	const color = tone === "live" ? "bg-chart-info" : tone === "good" ? "bg-chart-success" : tone === "bad" ? "bg-chart-error" : "bg-muted-foreground/60";
	return (
		<span className={cn("relative inline-flex size-2 shrink-0", className)} aria-hidden="true">
			{tone === "live" && <span className={cn("absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:animate-none", color)} />}
			<span className={cn("relative inline-flex size-2 rounded-full", color)} />
		</span>
	);
}

/** The experiment state as a soft pill with a dot. */
export function ExperimentStatePill({ state, testId }: { state: ExperimentStatus["state"]; testId?: string }) {
	const { t } = useTranslation("live");
	const tone = STATE_TONE[state] ?? "muted";
	return (
		<span
			className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap", TONE_PILL[tone])}
			data-testid={testId}
			data-state={state}
		>
			<LiveDot tone={tone} />
			{t(`state.${state}`, { defaultValue: state })}
		</span>
	);
}

/** Done, failed and running slots as one stacked bar. */
export function SlotBar({ slots, className, label }: { slots: ExperimentStatus["slots"]; className?: string; label?: string }) {
	const { total, done, running, failed } = slots;
	const share = (n: number) => (total > 0 ? `${Math.min(100, Math.max(0, (n / total) * 100))}%` : "0%");
	return (
		<div
			className={cn("bg-chart-track flex h-2 w-full overflow-hidden rounded-full", className)}
			role="progressbar"
			aria-valuemin={0}
			aria-valuemax={total}
			aria-valuenow={done + failed}
			aria-label={label}
		>
			<div className="bg-chart-success h-full transition-[width] duration-700 ease-out" style={{ width: share(done) }} />
			<div className="bg-chart-error h-full transition-[width] duration-700 ease-out" style={{ width: share(failed) }} />
			<div
				className="bg-chart-info/70 h-full animate-pulse transition-[width] duration-700 ease-out motion-reduce:animate-none"
				style={{ width: share(running) }}
			/>
		</div>
	);
}

/** One number with a small label, for the done / failed / spent row. */
export function LiveStat({ label, children, tone, testId }: { label: string; children: React.ReactNode; tone?: Tone; testId?: string }) {
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<span className="text-muted-foreground text-xs">{label}</span>
			<span
				className={cn(
					"text-base font-semibold tabular-nums tracking-tight",
					tone === "good" && "text-chart-success-ink",
					tone === "bad" && "text-chart-error-ink",
					tone === "live" && "text-chart-info-ink",
				)}
				data-testid={testId}
			>
				{children}
			</span>
		</div>
	);
}

export function Spend({ status, testId }: { status: ExperimentStatus; testId?: string }) {
	return <ValueCell value={status.spent_usd} kind="usd" testId={testId} />;
}

/** The slot's current stage: a spinner while running, an icon once finished. */
export function StageChip({ slot }: { slot: LiveSlotView }) {
	const { t } = useTranslation("live");
	let icon: React.ReactNode;
	let tone: Tone;
	let label: string;
	if (slot.state === "running") {
		icon = <Loader2 className="size-3 animate-spin motion-reduce:animate-none" aria-hidden="true" />;
		tone = "live";
		label = slot.stage ? t(`stage.${slot.stage}`, { defaultValue: slot.stage }) : t("slot.starting");
	} else if (slot.state === "done") {
		icon = <Check className="size-3" aria-hidden="true" />;
		tone = slot.outcome === "pass" ? "good" : "muted";
		label = slot.outcome ?? t("slot.done");
	} else if (slot.state === "failed") {
		icon = <X className="size-3" aria-hidden="true" />;
		tone = "bad";
		label = slot.outcome ?? t("slot.failed");
	} else {
		icon = <Ban className="size-3" aria-hidden="true" />;
		tone = "muted";
		label = t("slot.skipped");
	}
	return (
		<span
			className={cn("inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap", TONE_PILL[tone])}
			data-testid={`live-slot-stage-${slot.slotId}`}
			data-state={slot.state}
		>
			{icon}
			{label}
		</span>
	);
}

/** One slot: task, arm, stage chip and elapsed time. */
export function SlotRow({ slot, compact }: { slot: LiveSlotView; compact?: boolean }) {
	const { t } = useTranslation("live");
	const title = slot.task ?? t("progress.slotFallback", { id: slot.slotId.slice(0, 12) });
	return (
		<li
			className={cn(
				"animate-in fade-in flex min-w-0 items-center gap-3 rounded-lg px-3 py-2 transition-colors duration-300",
				slot.state === "running" ? "bg-chart-info/5" : "hover:bg-muted/50",
				compact && "px-2 py-1.5",
			)}
			data-testid={`live-slot-${slot.slotId}`}
			data-state={slot.state}
		>
			<div className="flex min-w-0 flex-1 flex-col">
				<span className="truncate text-sm font-medium" title={slot.task ?? slot.slotId}>
					{title}
				</span>
				{(slot.arm || (!compact && slot.message)) && (
					<span className="text-muted-foreground truncate text-xs" title={slot.message}>
						{slot.arm && <span className="text-foreground/80 font-mono">{slot.arm}</span>}
						{slot.arm && !compact && slot.message ? " · " : ""}
						{!compact && slot.message}
					</span>
				)}
			</div>
			<StageChip slot={slot} />
			{slot.elapsedMs !== undefined && (
				<span className="text-muted-foreground flex w-16 shrink-0 items-center justify-end gap-1 text-xs tabular-nums">
					<Clock className="size-3" aria-hidden="true" />
					{formatElapsed(slot.elapsedMs)}
				</span>
			)}
		</li>
	);
}

const EVENT_KINDS = new Set([
	"slot_started",
	"run_started",
	"stage",
	"slot_sealed",
	"run_sealed",
	"run_recovered",
	"slot_skipped",
	"lease_held",
	"budget_stop",
	"experiment_done",
	"cancelled",
	"error",
	"end",
]);

/** One event as a short readable line. */
export function useEventLabel(): (ev: LiveEvent) => string {
	const { t } = useTranslation("live");
	return (ev) => {
		const stage = ev.stage ? t(`stage.${ev.stage}`, { defaultValue: ev.stage }) : "";
		const base = EVENT_KINDS.has(ev.kind) ? t(`event.${ev.kind}`, { stage, defaultValue: ev.kind }) : ev.kind;
		const extra = ev.outcome ? ` (${ev.outcome})` : "";
		return ev.message ? `${base}${extra}: ${ev.message}` : `${base}${extra}`;
	};
}

function eventTime(at: string): string {
	const d = new Date(at);
	return Number.isFinite(d.getTime()) ? d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "";
}

/** The latest events, newest on top, each fading in as it arrives. */
export function EventTicker({ events, limit = 5, testId }: { events: readonly LiveEvent[]; limit?: number; testId?: string }) {
	const { t } = useTranslation("live");
	const label = useEventLabel();
	const shown = events.slice(-limit).reverse();
	if (shown.length === 0) {
		return (
			<p className="text-muted-foreground text-sm" data-testid={testId}>
				{t("progress.noEvents")}
			</p>
		);
	}
	return (
		<ol className="flex flex-col gap-1" aria-live="polite" data-testid={testId}>
			{shown.map((ev, i) => (
				<li
					key={`${ev.seq}-${ev.at}-${ev.kind}`}
					className={cn(
						"animate-in fade-in slide-in-from-top-1 flex min-w-0 items-baseline gap-2 text-xs duration-500",
						i === 0 ? "text-foreground" : "text-muted-foreground",
					)}
					style={{ opacity: 1 - i * 0.15 }}
				>
					{ev.kind === "error" ? (
						<CircleAlert className="text-chart-error-ink size-3 shrink-0 self-center" aria-hidden="true" />
					) : (
						<LiveDot tone={i === 0 ? "live" : "muted"} className="self-center" />
					)}
					<span className={cn("min-w-0 flex-1 truncate", ev.kind === "error" && "text-chart-error-ink")} title={label(ev)}>
						{label(ev)}
					</span>
					<time className="shrink-0 tabular-nums" dateTime={ev.at}>
						{eventTime(ev.at)}
					</time>
				</li>
			))}
		</ol>
	);
}
