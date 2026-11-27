import { DEFAULT_MIN_RUNS } from "@/components/leaderboard/agentBoard.utils";
import { formatValueNumber } from "@/components/valueCell.utils";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import type { Action, Decision, Ratio, Value, Verdict } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { cheaperArm, formatUsd2, observedNumber } from "./armCost";
import "@/components/experiments/ufrcI18n";
import { plainSetupsNote } from "./setupsTable";
import { ArrowRight, Ban, CircleCheck, CircleHelp, CircleX, Gauge, Hourglass, Trophy, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import "./uf4aI18n";
import "@/components/share/ufd4I18n";
import {
	DECISION_TONE,
	formatPp,
	guardTasks,
	isSetupsVerdict,
	joinSetups,
	metricUnit,
	formatSignedEffect,
	plainInvalidReason,
	rateOrTooFew,
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

// M1-30: A blue (--chart-1), B amber (--chart-2), told apart under deuteranopia; the letter sits by the bar.
const BAR = ["bg-chart-1", "bg-chart-2", "bg-chart-cat-3"] as const;

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
	/** arm -> metric -> median per run, from the diagnostics; tokens (and the median cost) come from here. */
	armMetrics?: Record<string, Record<string, Value>>;
	/**
	 * arm -> average (mean) cost per run, from the cost explanation. The one cost a setup is labelled
	 * with: it multiplies to the bill. The median stays as a labelled second value; without the mean
	 * the tile shows the median under its own label.
	 */
	armCost?: Record<string, Value>;
	/** Runs thrown out for cheating per arm, from the grid (M4-11); a note under the pass count when above 0. */
	thrown?: Record<string, number>;
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
	armCost,
	thrown,
	onNext,
	className,
}: VerdictSummaryProps) {
	const { t } = useTranslation("experimentPage");
	const { t: tr } = useTranslation("ufrc");
	const locale = getLocale();
	// No clear winner is not a warning (M4-12): the panel stays grey.
	const tone: Tone = partial || verdict.decision === "inconclusive" ? "neutral" : (DECISION_TONE[verdict.decision] ?? "neutral");
	const Icon = partial ? Hourglass : (ICON[verdict.decision] ?? CircleHelp);
	const setups = isSetupsVerdict(verdict);
	const panels = setups ? arms : arms.slice(0, 2);
	const safety = !partial && setups ? safetyLines(verdict, t) : [];
	const costs = costNumbers(panels, armCost, armMetrics);
	const cheaper = !partial && !setups ? cheaperArm(panels, costs) : null;
	const detail = !partial && !setups ? headlineDetail(panels, naive, costs, names, locale, t) : null;
	const hintText = hint(verdict, partial, t, tr as unknown as (k: string, o?: Record<string, unknown>) => string);

	return (
		<section
			className={cn("flex flex-col gap-6 rounded-xl border p-6", SURFACE[tone], className)}
			data-testid="verdict-summary"
			data-decision={partial ? "partial" : verdict.decision}
		>
			<div className="flex items-start gap-3">
				<Icon className={cn("mt-0.5 size-8 shrink-0", INK[tone])} strokeWidth={2.25} aria-hidden="true" />
				<div className="flex flex-col gap-1">
					<h2 className={cn("text-xl font-semibold tracking-tight sm:text-3xl", INK[tone])} data-testid="verdict-summary-headline">
						{headline(verdict, arms, { names, partial, costs, naive }, locale, t)}
					</h2>
					{detail && (
						<p className="text-foreground text-sm" data-testid="verdict-summary-detail">
							{detail}
						</p>
					)}
					{safety.map((line) => (
						<p key={line} className="text-chart-error-ink text-sm font-medium" data-testid="verdict-summary-safety">
							{line}
						</p>
					))}
					{hintText && (
						<p className="text-muted-foreground text-sm" data-testid="verdict-summary-hint">
							{hintText}
						</p>
					)}
				</div>
			</div>

			<div
				className={cn("grid grid-cols-1 gap-4 sm:grid-cols-2 sm:gap-0", panels.length > 2 && "lg:grid-cols-3")}
				data-testid="verdict-summary-arms"
			>
				{panels.map((arm, index) => (
					<ArmPanel
						key={arm}
						arm={arm}
						label={setupLabel(arm, names, t)}
						first={index === 0}
						cheaper={cheaper?.arm === arm}
						mean={armCost?.[arm]}
						thrown={thrown?.[arm] ?? 0}
						colour={BAR[index % BAR.length]}
						ratio={naive[arm]}
						metrics={armMetrics?.[arm]}
						status={armMetrics ? undefined : metricsStatus}
					/>
				))}
			</div>

			<div className="flex flex-col gap-3 border-t pt-3 sm:flex-row sm:items-center sm:justify-between" data-testid="verdict-summary-next">
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
	label,
	first,
	cheaper,
	mean,
	thrown = 0,
	colour,
	ratio,
	metrics,
	status,
}: {
	arm: string;
	/** "A (codex on gpt-5.6-terra)": the one name of the setup on every tab (M4-4). */
	label: string;
	first: boolean;
	cheaper: boolean;
	mean?: Value;
	thrown?: number;
	colour: string;
	ratio?: Ratio;
	metrics?: Record<string, Value>;
	status?: "loading" | "error";
}) {
	const { t } = useTranslation("experimentPage");
	const { t: tu } = useTranslation("uf4a");
	const { t: td } = useTranslation("ufd4");
	const { t: tr } = useTranslation("ufrc");
	const locale = getLocale();
	// Under 10 runs the rate is not a score: the same rule as the shared comparison page.
	const { rate: measured, tooFew } = rateOrTooFew(ratio);
	const rate = tooFew ? null : measured;
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
	const meanCost = observedNumber(mean);
	const medianCost = observedNumber(metrics?.cost_usd);
	// M4-2: "Average cost per run" (the mean) on every tab. Q-49: without the mean (three or more setups, or the cost
	// explanation is missing) a known median is the headline under its own label, never "Not known" above a number.
	const head = costHeadline(meanCost, medianCost);
	const costLabel = head === "median" ? tr("summary.medianCost") : t("summary.cost");
	const costText =
		head === "mean"
			? formatUsd2(meanCost as number, locale)
			: head === "median"
				? formatUsd2(medianCost as number, locale)
				: mean
					? notKnown(mean.reason, t)
					: value("cost_usd", "usd");
	return (
		<div
			className={cn("flex flex-col gap-3 py-1 sm:px-4", first ? "sm:pl-0" : "border-t pt-4 sm:border-t-0 sm:border-l sm:pt-1")}
			data-testid={`verdict-summary-arm-${arm}`}
		>
			<div className="flex items-baseline justify-between gap-2">
				<span className="min-w-0 text-sm font-semibold break-words" title={arm}>
					{label}
				</span>
				{tooFew ? (
					<span
						className="text-right text-sm font-semibold"
						title={ratio ? td("shared.tooFewDetail", { den: ratio.den, min: DEFAULT_MIN_RUNS }) : undefined}
						data-testid={`verdict-summary-too-few-${arm}`}
					>
						{td("shared.tooFew")}
					</span>
				) : (
					<span className="text-2xl font-semibold tabular-nums">
						{rate === null ? t("summary.unknown") : `${formatPp(rate * 100, locale)}%`}
					</span>
				)}
			</div>
			<div className={cn("flex items-center gap-2", tooFew && "hidden")}>
				<span className="text-muted-foreground w-3 shrink-0 font-mono text-xs font-semibold" aria-hidden="true">
					{arm.length <= 2 ? arm : arm.slice(0, 1).toUpperCase()}
				</span>
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
			</div>
			<span className="text-muted-foreground text-xs">
				{ratio ? t("summary.passCount", { num: ratio.num, den: ratio.den }) : t("summary.passRate")}
			</span>
			{thrown > 0 && (
				<Link
					to="."
					search={((prev: Record<string, unknown>) => ({ ...prev, tab: "runs", outcome: "integrity_violation" })) as never}
					className="text-warning-ink w-fit text-xs font-medium underline-offset-2 hover:underline"
					data-testid={`verdict-summary-thrown-${arm}`}
				>
					{tu("summary.thrown", { count: thrown })}
				</Link>
			)}
			<dl className="grid grid-cols-2 gap-2 text-xs">
				<div>
					<dt className="text-muted-foreground">{costLabel}</dt>
					<dd className="flex flex-wrap items-center gap-2">
						<span className="text-lg font-semibold tabular-nums" data-testid={`verdict-summary-cost-${arm}`}>
							{costText}
						</span>
						{cheaper && (
							<span
								className="bg-chart-success/10 text-chart-success-ink inline-flex min-h-6 items-center rounded-full px-2 py-1 text-xs font-medium"
								data-testid={`verdict-summary-cheaper-${arm}`}
							>
								{t("summary.cheaper")}
							</span>
						)}
					</dd>
					{head === "mean" && medianCost !== null && (
						<dd className="text-muted-foreground mt-0.5 text-xs tabular-nums" data-testid={`verdict-summary-median-${arm}`}>
							{t("summary.medianSecond", {
								cost: formatUsd2(medianCost, locale),
							})}
						</dd>
					)}
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

type HeadlineOptions = {
	names?: Record<string, string>;
	partial?: boolean;
	/** arm -> average cost per run (the mean, or the median when no mean is known). */
	costs?: Record<string, number | null>;
	naive?: Record<string, Ratio>;
};

/** Each setup's cost per run: the mean when known, else the diagnostics median. */
export function costNumbers(
	arms: readonly string[],
	armCost: Record<string, Value> | undefined,
	armMetrics: Record<string, Record<string, Value>> | undefined,
): Record<string, number | null> {
	const out: Record<string, number | null> = {};
	for (const arm of arms) out[arm] = observedNumber(armCost?.[arm]) ?? observedNumber(armMetrics?.[arm]?.cost_usd);
	return out;
}

/** The line under the headline: each setup's tasks solved and cost per run, or null when a pass count is missing. */
export function headlineDetail(
	arms: readonly string[],
	naive: Record<string, Ratio>,
	costs: Record<string, number | null>,
	names: Record<string, string> | undefined,
	locale: string,
	t: TFn,
): string | null {
	if (arms.length !== 2) return null;
	const parts: string[] = [];
	for (const arm of arms) {
		const ratio = naive[arm];
		if (!ratio) return null;
		const cost = costs[arm];
		const setup = setupLabel(arm, names, t);
		parts.push(
			cost == null
				? t("summary.headlineDetailNoCost", {
						setup,
						num: ratio.num,
						den: ratio.den,
					})
				: t("summary.headlineDetail", {
						setup,
						num: ratio.num,
						den: ratio.den,
						cost: formatUsd2(cost, locale),
					}),
		);
	}
	return parts.join(" ");
}

/** One line per setup that broke a safety rule in a setups verdict: "B broke a safety rule on t02." */
function safetyLines(verdict: Verdict, t: TFn): string[] {
	const safety = verdict.setups?.safety ?? {};
	return Object.keys(safety)
		.sort()
		.filter((arm) => (safety[arm] ?? []).length > 0)
		.map((arm) => t("summary.safetyLine", { setup: arm, tasks: safety[arm].join(", ") }));
}

function hint(
	verdict: Verdict,
	partial: boolean | undefined,
	t: TFn,
	tr: (k: string, o?: Record<string, unknown>) => string,
): string | null {
	if (partial) return t("summary.partialHint") || null;
	// Q-140: the server's setups note is statistics; the page says it in plain words.
	if (isSetupsVerdict(verdict))
		return verdict.decision === "invalid" ? t("summary.invalidHint") : verdict.setups ? plainSetupsNote(verdict.setups, tr) : null;
	if (verdict.decision === "inconclusive") return t("summary.inconclusiveHint") || null;
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
		case "inconclusive": {
			// Same score but one setup is much cheaper (M4-1): that is the news, not "no winner".
			const cheap = cheaperArm(arms, opts.costs);
			if (cheap) {
				const ra = opts.naive?.[arms[0]];
				const rb = opts.naive?.[arms[1]];
				const same = Boolean(ra && rb && ra.den > 0 && rb.den > 0 && ra.num * rb.den === rb.num * ra.den);
				const ratio = new Intl.NumberFormat(locale, {
					maximumSignificantDigits: 2,
				}).format(cheap.ratio);
				return t(same ? "summary.headline.sameScoreCheaper" : "summary.headline.unclearScoreCheaper", {
					setup: setupLabel(cheap.arm, opts.names, t),
					ratio,
				});
			}
			return t("summary.headline.inconclusive");
		}
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

/** Which cost a setup's tile leads with (Q-49): the mean, else a known median, else neither. Pure. */
export function costHeadline(mean: number | null, median: number | null): "mean" | "median" | "none" {
	if (mean !== null) return "mean";
	return median !== null ? "median" : "none";
}

/** Runs thrown out for cheating per arm, counted from the grid cells (M4-11). Pure. */
export function thrownByArm(
	rows: readonly { cells: readonly { arm: string; outcome: string | null }[] }[] | undefined,
): Record<string, number> {
	const out: Record<string, number> = {};
	for (const row of rows ?? [])
		for (const cell of row.cells) if (cell.outcome === "integrity_violation") out[cell.arm] = (out[cell.arm] ?? 0) + 1;
	return out;
}