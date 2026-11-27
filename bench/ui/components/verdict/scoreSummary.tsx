import { formatValueNumber } from "@/components/valueCell.utils";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import type { Ratio, Score, Value, Verdict } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ArrowRight, Ban, Gauge } from "lucide-react";
import { useTranslation } from "react-i18next";
import { formatScoreValue, isRateMetric, scoreBounds } from "./verdict.utils";
import { notKnown } from "./verdictSummary";

export interface ScoreSummaryProps {
	verdict: Verdict & { score: Score };
	/** arm -> raw passes over runs. */
	naive: Record<string, Ratio>;
	/** arm -> metric -> median per run, from the diagnostics; cost and tokens come from here. */
	armMetrics?: Record<string, Record<string, Value>>;
	/** Opens a comparison with another setup. */
	onCompare?: () => void;
	className?: string;
}

/**
 * A one-setup experiment's result: its score with a 95% range, how many runs were graded, and cost
 * and tokens. There is no second setup, so there is no better or worse and no release decision.
 */
export function ScoreSummary({ verdict, naive, armMetrics, onCompare, className }: ScoreSummaryProps) {
	const { t } = useTranslation("experimentPage");
	const locale = getLocale();
	const s = verdict.score;
	const invalid = verdict.decision === "invalid";
	const Icon = invalid ? Ban : Gauge;
	const value = (x: number | undefined) => (x === undefined ? t("summary.score.unknown") : formatScoreValue(s.metric, x, locale));
	const bounds = scoreBounds(s);
	const metrics = armMetrics?.[s.arm];
	const ratio = naive[s.arm];
	const metric = (name: string, kind: "usd" | "tokens") => {
		const v = metrics?.[name];
		if (v && v.state === "observed" && typeof v.v === "number") return formatValueNumber(v.v, kind, locale);
		return notKnown(v?.reason, t);
	};
	const pct = s.mean === undefined ? null : Math.min(100, Math.max(0, s.mean * 100));
	const rate = isRateMetric(s.metric);

	return (
		<section
			className={cn(
				"flex flex-col gap-6 rounded-xl border p-6",
				invalid ? "border-border bg-muted/50" : "border-border bg-card",
				className,
			)}
			data-testid="score-summary"
			data-decision={verdict.decision}
		>
			<div className="flex items-start gap-3">
				<Icon className="text-muted-foreground mt-1 size-6 shrink-0" strokeWidth={2.25} aria-hidden="true" />
				<div className="flex min-w-0 flex-col gap-1">
					<span className="text-muted-foreground text-sm">
						{t("summary.score.title")}
						{s.label ? <span className="text-foreground font-medium"> · {s.label}</span> : null}
					</span>
					<span className="text-4xl font-semibold tracking-tight tabular-nums" data-testid="score-summary-value">
						{value(s.mean)}
					</span>
					<span className="text-muted-foreground text-sm tabular-nums" data-testid="score-summary-interval">
						{s.interval
							? t("summary.score.interval", { lo: value(s.interval[0]), hi: value(s.interval[1]) })
							: t("summary.score.noInterval")}
					</span>
				</div>
			</div>

			{rate && pct !== null && (
				<div
					className="bg-muted relative h-2.5 w-full overflow-hidden rounded-full"
					role="meter"
					aria-label={t("summary.score.title")}
					aria-valuemin={0}
					aria-valuemax={100}
					aria-valuenow={Math.round(pct)}
				>
					{s.interval && (
						<div
							className="bg-chart-cat-1/25 absolute inset-y-0"
							style={{
								left: `${Math.max(0, s.interval[0] * 100)}%`,
								width: `${Math.max(0, Math.min(100, (s.interval[1] - s.interval[0]) * 100))}%`,
							}}
							data-testid="score-summary-range"
						/>
					)}
					<div className="bg-chart-cat-1 absolute inset-y-0 left-0 rounded-full" style={{ width: `${pct}%` }} />
				</div>
			)}

			<dl className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
				<div>
					<dt className="text-muted-foreground">{t("summary.passRate")}</dt>
					<dd className="text-sm font-medium tabular-nums">
						{ratio ? t("summary.passCount", { num: ratio.num, den: ratio.den }) : t("summary.unknown")}
					</dd>
				</div>
				<div>
					<dt className="text-muted-foreground">
						{/* The diagnostics value is a median (M4-2): labelled as one until the server sends a mean. */}
						{t("summary.costMedian")}
					</dt>
					<dd className="text-sm font-medium tabular-nums" data-testid="score-summary-cost">
						{metric("cost_usd", "usd")}
					</dd>
				</div>
				<div>
					<dt className="text-muted-foreground">{t("summary.tokens")}</dt>
					<dd className="text-sm font-medium tabular-nums">{metric("tokens", "tokens")}</dd>
				</div>
				<div>
					<dt className="sr-only">{t("summary.score.title")}</dt>
					<dd className="text-muted-foreground" data-testid="score-summary-runs">
						{t("summary.score.runs", { observed: s.observed, runs: s.runs, tasks: s.tasks })}
					</dd>
				</div>
			</dl>

			{bounds && (
				<p className="text-muted-foreground text-xs" data-testid="score-summary-bounds">
					{t("summary.score.bounds", { count: s.runs - s.observed, lo: value(bounds[0]), hi: value(bounds[1]) })}
				</p>
			)}

			<div className="bg-muted/40 flex flex-col gap-3 rounded-lg border px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
				<p className="text-sm" data-testid="score-summary-hint">
					{invalid ? t("summary.score.invalidHint") : t("summary.score.hint")}
				</p>
				{onCompare && (
					<Button size="sm" variant="outline" onClick={onCompare} dataTestId="score-summary-compare">
						{t("summary.score.compare")}
						<ArrowRight aria-hidden="true" />
					</Button>
				)}
			</div>
		</section>
	);
}