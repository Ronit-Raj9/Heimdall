import { formatValueNumber } from "@/components/valueCell.utils";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import type { Action, Decision, Ratio, Value, Verdict } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ArrowRight, Ban, CircleCheck, CircleHelp, CircleX, Gauge, Hourglass, Trophy, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
	DECISION_TONE,
	formatPp,
	guardTasks,
	isSetupsVerdict,
	joinSetups,
	metricUnit,
	formatSignedEffect,
	plainInvalidReason,
	type Tone,
} from "./verdict.utils";

const ICON = {
	improved: CircleCheck,
	regressed: CircleX,
	inconclusive: CircleHelp,
	invalid: Ban,
	score: Gauge,
	leader: Trophy,
} as const satisfies Record<Decision, LucideIcon>;

const SURFACE = {
	good: "border-chart-success/40 bg-chart-success/[0.06]",
	bad: "border-chart-error/40 bg-chart-error/[0.06]",
	warn: "border-chart-warning/45 bg-chart-warning/[0.07]",
	neutral: "border-border bg-muted/50",
} as const satisfies Record<Tone, string>;

const INK = {
	good: "text-chart-success-ink",
	bad: "text-chart-error-ink",
	warn: "text-chart-warning-ink",
	neutral: "text-foreground",
} as const satisfies Record<Tone, string>;

const BAR = ["bg-chart-cat-1", "bg-chart-cat-2", "bg-chart-cat-3"] as const;

export interface VerdictSummaryProps {
	verdict: Verdict;
	/** Arm A then arm B; every setup of a setups verdict. */
	arms: string[];
	/** arm -> the setup's display name ("codex on gpt-6.2-sol"); a name equal to the letter is not repeated. */
	names?: Record<string, string>;
	/** True while the comparison still runs (the verdict payload's partial): no decision is shown yet. */
	partial?: boolean;
	/** arm -> raw passes over runs. */
	naive: Record<string, Ratio>;
	/** arm -> metric -> median per run, from the diagnostics; cost and tokens come from here. */
	armMetrics?: Record<string, Record<string, Value>>;
	/** Where the diagnostics query is when armMetrics is absent: still loading, or failed. */
	metricsStatus?: "loading" | "error";
	/** The one next step; the page decides where the button goes. */
	onNext?: (action: Action) => void;
	className?: string;
}

/** The verdict in five seconds: one headline, A vs B pass rates (every setup's for three or more), cost and tokens, and what to do next. */
export function VerdictSummary({
	verdict,
	arms,
	names,
	partial,
	naive,
	armMetrics,
	metricsStatus,
	onNext,
	className,
}: VerdictSummaryProps) {
	const { t } = useTranslation("experimentPage");
	const locale = getLocale();
	const tone: Tone = partial ? "neutral" : (DECISION_TONE[verdict.decision] ?? "neutral");
	const Icon = partial ? Hourglass : (ICON[verdict.decision] ?? CircleHelp);
	const setups = isSetupsVerdict(verdict);
	const panels = setups ? arms : arms.slice(0, 2);
	const safety = !partial && setups ? safetyLines(verdict, t) : [];

	return (
		<section
			className={cn("flex flex-col gap-6 rounded-xl border p-6", SURFACE[tone], className)}
			data-testid="verdict-summary"
			data-decision={partial ? "partial" : verdict.decision}
		>
			<div className="flex items-start gap-3">
				<Icon className={cn("mt-0.5 size-8 shrink-0", INK[tone])} strokeWidth={2.25} aria-hidden="true" />
				<div className="flex flex-col gap-1">
					<h2 className={cn("text-2xl font-semibold tracking-tight sm:text-3xl", INK[tone])} data-testid="verdict-summary-headline">
						{headline(verdict, arms, { names, partial }, locale, t)}
					</h2>
					{safety.map((line) => (
						<p key={line} className="text-chart-error-ink text-sm font-medium" data-testid="verdict-summary-safety">
							{line}
						</p>
					))}
					{hint(verdict, partial, t) && (
						<p className="text-muted-foreground text-sm" data-testid="verdict-summary-hint">
							{hint(verdict, partial, t)}
						</p>
					)}
				</div>
			</div>

			<div
				className={cn("grid grid-cols-1 gap-4 sm:grid-cols-2", panels.length > 2 && "lg:grid-cols-3")}
				data-testid="verdict-summary-arms"
			>
				{panels.map((arm, index) => (
					<ArmPanel
						key={arm}
						arm={arm}
						colour={BAR[index % BAR.length]}
						ratio={naive[arm]}
						metrics={armMetrics?.[arm]}
						status={armMetrics ? undefined : metricsStatus}
					/>
				))}
			</div>

			<div
				className="bg-card flex flex-col gap-3 rounded-lg border px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
				data-testid="verdict-summary-next"
			>
				<p className="text-sm">{nextLine(verdict, arms, { names, partial }, t)}</p>
				{onNext && !partial && (
					<Button size="sm" onClick={() => onNext(verdict.action)} dataTestId="verdict-summary-next-button">
						{t(`summary.button.${verdict.action}`)}
						<ArrowRight aria-hidden="true" />
					</Button>
				)}
			</div>
		</section>
	);
}

function ArmPanel({
	arm,
	colour,
	ratio,
	metrics,
	status,
}: {
	arm: string;
	colour: string;
	ratio?: Ratio;
	metrics?: Record<string, Value>;
	status?: "loading" | "error";
}) {
	const { t } = useTranslation("experimentPage");
	const locale = getLocale();
	const rate = ratio && ratio.den > 0 ? ratio.num / ratio.den : null;
	const value = (name: string, kind: "usd" | "tokens") => {
		const v = metrics?.[name];
		if (v && v.state === "observed" && typeof v.v === "number") {
			// Tokens per run in full (23,128), not compact: the panel has room and the exact count is what people compare.
			return kind === "tokens"
				? new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(v.v)
				: formatValueNumber(v.v, kind, locale);
		}
		if (status === "loading") return t("summary.metricsLoading");
		if (status === "error") return kind === "usd" ? t("summary.metricsError") : t("summary.unknown");
		return notKnown(v?.reason, t);
	};
	return (
		<div className="bg-card flex flex-col gap-3 rounded-lg border p-4" data-testid={`verdict-summary-arm-${arm}`}>
			<div className="flex items-baseline justify-between gap-2">
				<span className="truncate font-mono text-sm font-semibold" title={arm}>
					{arm}
				</span>
				<span className="text-2xl font-semibold tabular-nums">
					{rate === null ? t("summary.unknown") : `${formatPp(rate * 100, locale)}%`}
				</span>
			</div>
			<div
				className="bg-muted h-2.5 w-full overflow-hidden rounded-full"
				role="meter"
				aria-label={t("summary.passRate")}
				aria-valuemin={0}
				aria-valuemax={100}
				aria-valuenow={rate === null ? undefined : Math.round(rate * 100)}
			>
				<div
					className={cn("h-full rounded-full transition-all", colour)}
					style={{ width: `${rate === null ? 0 : Math.min(100, rate * 100)}%` }}
				/>
			</div>
			<span className="text-muted-foreground text-xs">
				{ratio ? t("summary.passCount", { num: ratio.num, den: ratio.den }) : t("summary.passRate")}
			</span>
			<dl className="grid grid-cols-2 gap-2 text-xs">
				<div>
					<dt className="text-muted-foreground">{t("summary.cost")}</dt>
					<dd className="text-sm font-medium tabular-nums" data-testid={`verdict-summary-cost-${arm}`}>
						{value("cost_usd", "usd")}
					</dd>
				</div>
				<div>
					<dt className="text-muted-foreground">{t("summary.tokens")}</dt>
					<dd className="text-sm font-medium tabular-nums" data-testid={`verdict-summary-tokens-${arm}`}>
						{value("tokens", "tokens")}
					</dd>
				</div>
			</dl>
		</div>
	);
}

type TFn = ReturnType<typeof useTranslation<"experimentPage">>["t"];

/** A value that is not known, with its own reason; the missing-price sentence only for unpriced cost rows. */
export function notKnown(reason: string | undefined, t: TFn): string {
	if (!reason) return t("summary.unknown");
	if (reason.includes("unpriced")) return t("summary.costUnknown");
	return t("summary.notKnownReason", { reason });
}

function magnitude(verdict: Verdict, locale: string): string | null {
	const effect = verdict.effect;
	if (!effect) return null;
	const size = Math.abs(effect.d_bar);
	return metricUnit(effect.metric) === null ? `${formatPp(size * 100, locale)} pp` : formatSignedEffect(effect.metric, size, locale);
}

/** How a setup is named in a sentence: "B (codex on gpt-6.2-sol)", or only the letter when the name is the letter or unknown. */
function setupLabel(arm: string, names: Record<string, string> | undefined, t: TFn): string {
	const name = names?.[arm]?.trim();
	return name && name !== arm ? t("summary.setupLabel", { arm, name }) : arm;
}

type HeadlineOptions = { names?: Record<string, string>; partial?: boolean };

/** One line per setup that broke a safety rule in a setups verdict: "B broke a safety rule on t02." */
function safetyLines(verdict: Verdict, t: TFn): string[] {
	const safety = verdict.setups?.safety ?? {};
	return Object.keys(safety)
		.sort()
		.filter((arm) => (safety[arm] ?? []).length > 0)
		.map((arm) => t("summary.safetyLine", { setup: arm, tasks: safety[arm].join(", ") }));
}

function hint(verdict: Verdict, partial: boolean | undefined, t: TFn): string | null {
	if (partial) return t("summary.partialHint");
	if (isSetupsVerdict(verdict)) return verdict.decision === "invalid" ? t("summary.invalidHint") : verdict.setups.note || null;
	if (verdict.decision === "inconclusive") return t("summary.inconclusiveHint");
	if (verdict.decision === "invalid" && !verdict.invalid_reasons?.length) return t("summary.invalidHint");
	return null;
}

/**
 * The headline. Two setups: "B (name) is better by 12 pp", or "B broke a safety rule on t02" when a
 * guard, not a significant effect, decided a regression (basis guard). Three or more: "Cannot tell
 * A, B and C apart yet" or "A is ahead of every other setup". While running: "Still running: no
 * result yet". A broken test names why.
 */
export function headline(verdict: Verdict, arms: readonly string[], opts: HeadlineOptions, locale: string, t: TFn): string {
	if (opts.partial) return t("summary.headline.partial");
	const b = arms[1];
	const setup = setupLabel(b ?? "B", opts.names, t);
	const pp = magnitude(verdict, locale);
	if (verdict.decision === "invalid") {
		const why = verdict.invalid_reasons?.[0];
		return why ? t("summary.headline.invalidWhy", { why: plainInvalidReason(why) }) : t("summary.headline.invalid");
	}
	if (isSetupsVerdict(verdict)) {
		const s = verdict.setups;
		if (verdict.decision === "leader") return t("summary.headline.setupsLeader", { leader: setupLabel(s.leader, opts.names, t) });
		return t("summary.headline.setupsTied", { setups: joinSetups(arms.length > 0 ? arms : s.top_group) });
	}
	switch (verdict.decision) {
		case "improved":
			return pp ? t("summary.headline.improved", { setup, pp }) : t("summary.headline.improvedNoEffect", { setup });
		case "regressed": {
			if (verdict.basis === "guard") {
				const failed = verdict.guards.find((g) => g.status === "fail");
				const tasks = guardTasks(failed?.detail);
				if (tasks) return t("summary.headline.regressedGuard", { setup, tasks });
				return t("summary.headline.regressedGuardDetail", { setup, detail: failed?.detail ?? "" });
			}
			return pp ? t("summary.headline.regressed", { setup, pp }) : t("summary.headline.regressedNoEffect", { setup });
		}
		case "inconclusive":
			return t("summary.headline.inconclusive");
		case "score":
			return t("summary.score.title");
	}
	return arms[0] ?? "";
}

function nextLine(verdict: Verdict, arms: readonly string[], opts: HeadlineOptions, t: TFn): string {
	if (opts.partial) return t("summary.next.partial");
	if (isSetupsVerdict(verdict) && verdict.decision !== "invalid") {
		const s = verdict.setups;
		if (verdict.decision !== "leader") return t("summary.next.setupsTied");
		const leader = setupLabel(s.leader, opts.names, t);
		const flagged = s.safety?.[s.leader]?.length ?? 0;
		return flagged > 0 ? t("summary.next.setupsLeaderFlagged", { leader, count: flagged }) : t("summary.next.setupsLeader", { leader });
	}
	switch (verdict.action) {
		case "ship":
			return t("summary.next.ship", { arm: setupLabel(arms[1] ?? "B", opts.names, t) });
		case "hold":
			return verdict.decision === "regressed" && verdict.basis !== "guard"
				? t("summary.next.holdRegressed", { arm: setupLabel(arms[0] ?? "A", opts.names, t) })
				: t("summary.next.holdViolations");
		case "no_decision":
			return t("summary.next.no_decision");
		case "fix_harness":
			return t("summary.next.fix_harness");
	}
	return "";
}