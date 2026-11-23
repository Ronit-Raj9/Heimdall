import type { Decision, ExperimentState } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Ban, CircleCheck, CircleHelp, CircleX, Gauge, Trophy, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import "@/components/verdict/hw09I18n";

// Same status tokens as the verdict card and the experiment progress card (globals.css), so a
// badge reads the same on every page in light and dark mode.
const STATE_CLASS = {
	created: "border-border bg-muted/50 text-muted-foreground",
	running: "border-chart-info/60 bg-chart-info/10 text-chart-info-ink",
	analyzing: "border-chart-info/60 bg-chart-info/10 text-chart-info-ink",
	done: "border-chart-success/50 bg-chart-success/10 text-chart-success-ink",
	failed: "border-chart-error/50 bg-chart-error/10 text-chart-error-ink",
	cancelled: "border-border bg-muted/50 text-muted-foreground",
} as const satisfies Record<ExperimentState, string>;

const DECISION_CLASS = {
	improved: "border-chart-success/50 bg-chart-success/10 text-chart-success-ink",
	regressed: "border-chart-error/50 bg-chart-error/10 text-chart-error-ink",
	inconclusive: "border-chart-warning/60 bg-chart-warning/10 text-chart-warning-ink",
	invalid: "border-border bg-muted text-muted-foreground",
	score: "border-chart-info/50 bg-chart-info/10 text-chart-info-ink",
	leader: "border-chart-success/50 bg-chart-success/10 text-chart-success-ink",
} as const satisfies Record<Decision, string>;

// The decision's shape, so it reads without colour (as on the verdict card).
const DECISION_ICON = {
	improved: CircleCheck,
	regressed: CircleX,
	inconclusive: CircleHelp,
	invalid: Ban,
	score: Gauge,
	leader: Trophy,
} as const satisfies Record<Decision, LucideIcon>;

const LIVE_STATES: ReadonlySet<ExperimentState> = new Set(["running", "analyzing"]);

export type StatusBadgeProps = {
	className?: string;
	testId?: string;
} & ({ state: ExperimentState | string; decision?: never } | { decision: Decision | string; state?: never });

/**
 * True for the verdict decisions the server sends (score: a one-setup experiment, scored on its own;
 * leader: one of three or more setups ahead of every other).
 */
export function isDecision(value: string | undefined | null): value is Decision {
	return (
		value === "improved" ||
		value === "regressed" ||
		value === "inconclusive" ||
		value === "invalid" ||
		value === "score" ||
		value === "leader"
	);
}

/** True for the experiment states the server sends. */
export function isExperimentState(value: string | undefined | null): value is ExperimentState {
	return typeof value === "string" && value in STATE_CLASS;
}

/**
 * An experiment's state (created, running, analyzing, done, failed, cancelled) or a verdict
 * decision (improved, regressed, inconclusive, invalid) as a small badge. A running state carries
 * a pulsing dot; a decision carries its icon, so neither relies on colour alone. A value the server
 * may add later renders as a neutral badge with the raw word.
 */
export function StatusBadge(props: StatusBadgeProps) {
	const { t } = useTranslation("bench");
	const { t: t9 } = useTranslation("hw09");
	const base =
		"inline-flex h-5 shrink-0 items-center gap-1 rounded-[5px] border px-1.5 text-xs leading-4 font-medium whitespace-nowrap [&>svg]:size-3";

	if (props.decision !== undefined) {
		const value = props.decision;
		if (!isDecision(value)) {
			return (
				<span className={cn(base, "border-border text-muted-foreground", props.className)} data-testid={props.testId} data-decision={value}>
					{value || t("shell.status.noDecision")}
				</span>
			);
		}
		const Icon = DECISION_ICON[value];
		return (
			<span className={cn(base, DECISION_CLASS[value], props.className)} data-testid={props.testId} data-decision={value}>
				<Icon aria-hidden="true" strokeWidth={2.25} />
				{value === "leader" ? t9("decision.leader") : t(`shell.status.decision.${value}`)}
			</span>
		);
	}

	const state = props.state;
	if (!isExperimentState(state)) {
		return (
			<span className={cn(base, "border-border text-muted-foreground", props.className)} data-testid={props.testId} data-state={state}>
				{state}
			</span>
		);
	}
	const live = LIVE_STATES.has(state);
	return (
		<span className={cn(base, STATE_CLASS[state], props.className)} data-testid={props.testId} data-state={state}>
			{live && (
				<span aria-hidden="true" className="relative flex size-1.5">
					<span className="bg-chart-info absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:animate-none" />
					<span className="bg-chart-info relative inline-flex size-1.5 rounded-full" />
				</span>
			)}
			{t(`shell.status.state.${state}`)}
		</span>
	);
}