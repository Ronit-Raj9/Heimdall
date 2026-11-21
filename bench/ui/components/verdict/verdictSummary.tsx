import { formatValueNumber } from "@/components/valueCell.utils";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import type { Action, Decision, Ratio, Value, Verdict } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ArrowRight, Ban, CircleCheck, CircleHelp, CircleX, Gauge, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import { DECISION_TONE, formatPp, metricUnit, formatSignedEffect, type Tone } from "./verdict.utils";

const ICON = {
	improved: CircleCheck,
	regressed: CircleX,
	inconclusive: CircleHelp,
	invalid: Ban,
	score: Gauge,
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

const BAR = ["bg-chart-cat-1", "bg-chart-cat-2"] as const;

export interface VerdictSummaryProps {
	verdict: Verdict;
	/** Arm A then arm B. */
	arms: string[];
	/** arm -> raw passes over runs. */
	naive: Record<string, Ratio>;
	/** arm -> metric -> median per run, from the diagnostics; cost and tokens come from here. */
	armMetrics?: Record<string, Record<string, Value>>;
	/** The one next step; the page decides where the button goes. */
	onNext?: (action: Action) => void;
	className?: string;
}

/** The verdict in five seconds: one headline, A vs B pass rates, cost and tokens, and what to do next. */
export function VerdictSummary({ verdict, arms, naive, armMetrics, onNext, className }: VerdictSummaryProps) {
	const { t } = useTranslation("experimentPage");
	const locale = getLocale();
	const tone = DECISION_TONE[verdict.decision];
	const Icon = ICON[verdict.decision];
	const [a, b] = arms;

	return (
		<section
			className={cn("flex flex-col gap-6 rounded-xl border p-6", SURFACE[tone], className)}
			data-testid="verdict-summary"
			data-decision={verdict.decision}
		>
			<div className="flex items-start gap-3">
				<Icon className={cn("mt-0.5 size-8 shrink-0", INK[tone])} strokeWidth={2.25} aria-hidden="true" />
				<div className="flex flex-col gap-1">
					<h2 className={cn("text-2xl font-semibold tracking-tight sm:text-3xl", INK[tone])} data-testid="verdict-summary-headline">
						{headline(verdict, a, b, locale, t)}
					</h2>
					{(verdict.decision === "inconclusive" || verdict.decision === "invalid") && (
						<p className="text-muted-foreground text-sm" data-testid="verdict-summary-hint">
							{t(verdict.decision === "inconclusive" ? "summary.inconclusiveHint" : "summary.invalidHint")}
						</p>
					)}
				</div>
			</div>

			<div className="grid grid-cols-1 gap-4 sm:grid-cols-2" data-testid="verdict-summary-arms">
				{arms.slice(0, 2).map((arm, index) => (
					<ArmPanel key={arm} arm={arm} colour={BAR[index]} ratio={naive[arm]} metrics={armMetrics?.[arm]} />
				))}
			</div>

			<div className="bg-card flex flex-col gap-3 rounded-lg border px-4 py-3 sm:flex-row sm:items-center sm:justify-between" data-testid="verdict-summary-next">
				<p className="text-sm">{nextLine(verdict, a, b, t)}</p>
				{onNext && (
					<Button size="sm" onClick={() => onNext(verdict.action)} dataTestId="verdict-summary-next-button">
						{t(`summary.button.${verdict.action}`)}
						<ArrowRight aria-hidden="true" />
					</Button>
				)}
			</div>
		</section>
	);
}

function ArmPanel({ arm, colour, ratio, metrics }: { arm: string; colour: string; ratio?: Ratio; metrics?: Record<string, Value> }) {
	const { t } = useTranslation("experimentPage");
	const locale = getLocale();
	const rate = ratio && ratio.den > 0 ? ratio.num / ratio.den : null;
	const value = (name: string, kind: "usd" | "tokens") => {
		const v = metrics?.[name];
		if (v && v.state === "observed" && typeof v.v === "number") return formatValueNumber(v.v, kind, locale);
		// An unknown cost almost always means the model has no price in the catalog.
		return kind === "usd" ? t("summary.costUnknown") : t("summary.unknown");
	};
	return (
		<div className="bg-card flex flex-col gap-3 rounded-lg border p-4" data-testid={`verdict-summary-arm-${arm}`}>
			<div className="flex items-baseline justify-between gap-2">
				<span className="truncate font-mono text-sm font-semibold" title={arm}>
					{arm}
				</span>
				<span className="text-2xl font-semibold tabular-nums">{rate === null ? t("summary.unknown") : `${formatPp(rate * 100, locale)}%`}</span>
			</div>
			<div
				className="bg-muted h-2.5 w-full overflow-hidden rounded-full"
				role="meter"
				aria-label={t("summary.passRate")}
				aria-valuemin={0}
				aria-valuemax={100}
				aria-valuenow={rate === null ? undefined : Math.round(rate * 100)}
			>
				<div className={cn("h-full rounded-full transition-all", colour)} style={{ width: `${rate === null ? 0 : Math.min(100, rate * 100)}%` }} />
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

function magnitude(verdict: Verdict, locale: string): string | null {
	const effect = verdict.effect;
	if (!effect) return null;
	const size = Math.abs(effect.d_bar);
	return metricUnit(effect.metric) === null ? `${formatPp(size * 100, locale)} pp` : formatSignedEffect(effect.metric, size, locale);
}

export function headline(verdict: Verdict, a: string | undefined, b: string | undefined, locale: string, t: TFn): string {
	const arm = b ?? "B";
	const pp = magnitude(verdict, locale);
	switch (verdict.decision) {
		case "improved":
			return pp ? t("summary.headline.improved", { arm, pp }) : t("summary.headline.improvedNoEffect", { arm });
		case "regressed":
			return pp ? t("summary.headline.regressed", { arm, pp }) : t("summary.headline.regressedNoEffect", { arm });
		case "inconclusive":
			return t("summary.headline.inconclusive");
		case "invalid":
			return t("summary.headline.invalid");
		case "score":
			return t("summary.score.title");
	}
	return a ?? "";
}

function nextLine(verdict: Verdict, a: string | undefined, b: string | undefined, t: TFn): string {
	switch (verdict.action) {
		case "ship":
			return t("summary.next.ship", { arm: b ?? "B" });
		case "hold":
			return verdict.decision === "regressed" ? t("summary.next.holdRegressed", { arm: a ?? "A" }) : t("summary.next.holdViolations");
		case "no_decision":
			return t("summary.next.no_decision");
		case "fix_harness":
			return t("summary.next.fix_harness");
	}
}
