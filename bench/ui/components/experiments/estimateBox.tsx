import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatValueNumber } from "@/components/valueCell.utils";
import { TONE_CLASS } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { Forecast, ForecastRange, ValueKind } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { FIT_TONE, forecastFit, forecastRangeView } from "./estimate.utils";

/**
 * POST /estimate beside the plan: total agent cost, agent time and wall clock as planning ranges,
 * the budget fit with its reason, where the numbers come from, and the same per arm. A range the
 * server cannot estimate says why instead of showing a number.
 */
export function EstimateBox({ forecast, className }: { forecast: Forecast; className?: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const fit = forecastFit(forecast.fit);
	const arms = forecast.arms ?? [];
	const notes = forecast.notes ?? [];

	return (
		<div className={cn("flex flex-col gap-3", className)} data-testid="estimate-box" data-fit={fit}>
			<div className="flex flex-wrap items-center gap-2">
				<span className={cn("rounded-sm border px-1.5 py-0.5 text-xs font-semibold", TONE_CLASS[FIT_TONE[fit]])} data-testid="estimate-fit">
					{t(`experiments.new.estimate.fit.${fit}`)}
				</span>
				{forecast.budget_usd > 0 && (
					<span className="text-muted-foreground text-xs tabular-nums" data-testid="estimate-budget">
						{t("experiments.new.estimate.budget", { budget: formatValueNumber(forecast.budget_usd, "usd", locale) })}
					</span>
				)}
			</div>
			{forecast.fit_reason && (
				<p className="text-sm" data-testid="estimate-fit-reason">
					{forecast.fit_reason}
				</p>
			)}

			<dl className="grid grid-cols-1 gap-2">
				<Stat label={t("experiments.new.estimate.cost")} testId="estimate-cost">
					<RangeText range={forecast.cost_usd} kind="usd" testId="estimate-cost-value" />
				</Stat>
				<Stat label={t("experiments.new.estimate.agentTime")} testId="estimate-agent-time">
					<RangeText range={forecast.agent_time_s} kind="duration" testId="estimate-agent-time-value" />
				</Stat>
				<Stat label={t("experiments.new.estimate.wallClock")} testId="estimate-wall-clock">
					<RangeText range={forecast.wall_clock_s} kind="duration" testId="estimate-wall-clock-value" />
					{forecast.concurrency > 0 && (
						<span className="text-muted-foreground mt-0.5 block text-xs font-normal">
							{t("experiments.new.estimate.wallClockHint", { concurrency: forecast.concurrency })}
						</span>
					)}
				</Stat>
			</dl>

			{forecast.basis && (
				<p className="text-muted-foreground text-xs" data-testid="estimate-basis">
					{forecast.basis}
				</p>
			)}

			{arms.length > 0 && (
				<div className="flex flex-col gap-1.5">
					<span className="text-muted-foreground text-xs font-medium">{t("experiments.new.estimate.perArm")}</span>
					<Table className="text-xs" data-testid="estimate-arms">
						<TableHeader>
							<TableRow>
								<TableHead className="h-8 px-2">{t("experiments.new.estimate.arm")}</TableHead>
								<TableHead className="h-8 px-2 text-right">{t("experiments.new.estimate.runs")}</TableHead>
								<TableHead className="h-8 px-2">{t("experiments.new.estimate.basis")}</TableHead>
								<TableHead className="h-8 px-2 text-right">{t("experiments.new.estimate.armCost")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{arms.map((arm) => (
								<TableRow key={arm.arm} data-testid={`estimate-arm-${arm.arm}`} data-basis={arm.basis}>
									<TableCell className="px-2 align-top">
										<div className="flex flex-col gap-0.5">
											<span className="font-mono font-semibold">{arm.arm}</span>
											{arm.label && (
												<span className="max-w-40 truncate" title={arm.label}>
													{arm.label}
												</span>
											)}
											<span className="text-muted-foreground font-mono text-[11px]">{arm.model}</span>
										</div>
									</TableCell>
									<TableCell className="px-2 text-right align-top tabular-nums">{arm.runs}</TableCell>
									<TableCell className="text-muted-foreground px-2 align-top whitespace-normal">{arm.basis_text}</TableCell>
									<TableCell className="px-2 text-right align-top whitespace-normal">
										<RangeText range={arm.cost_usd} kind="usd" testId={`estimate-arm-cost-${arm.arm}`} />
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				</div>
			)}

			{forecast.judge && (
				<p className="text-xs" data-testid="estimate-judge">
					{t("experiments.new.estimate.judge", {
						runs: forecast.judge.runs,
						cost: rangeString(forecast.judge.cost_usd, "usd", locale, t),
					})}{" "}
					<span className="text-muted-foreground">{forecast.judge.basis_text}</span>
				</p>
			)}

			{notes.length > 0 && (
				<div className="flex flex-col gap-1" data-testid="estimate-notes">
					<span className="text-muted-foreground text-xs font-medium">{t("experiments.new.estimate.notes")}</span>
					<ul className="text-muted-foreground flex list-disc flex-col gap-0.5 pl-4 text-xs">
						{notes.map((note) => (
							<li key={note} className="break-words">
								{note}
							</li>
						))}
					</ul>
				</div>
			)}
			{forecast.price_ref && (
				<p className="text-muted-foreground text-[11px]" data-testid="estimate-price-ref">
					{t("experiments.new.estimate.priceRef", { ref: forecast.price_ref })}
				</p>
			)}
		</div>
	);
}

/** One range as words: "low to high (median m)", "at most high", or why it is unknown. */
function rangeString(range: ForecastRange | null | undefined, kind: ValueKind, locale: string, t: TFunction<"bench">): string {
	const view = forecastRangeView(range, kind, locale);
	switch (view.kind) {
		case "history":
			return t("experiments.new.estimate.range.history", { low: view.low, high: view.high, median: view.median });
		case "upper_bound":
			return t("experiments.new.estimate.range.upperBound", { high: view.high });
		case "unknown":
			return view.reason ?? t("experiments.new.estimate.range.unknown");
	}
}

function RangeText({ range, kind, testId }: { range: ForecastRange | null | undefined; kind: ValueKind; testId: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const view = forecastRangeView(range, kind, locale);
	return (
		<span
			className={cn("tabular-nums", view.kind === "unknown" && "text-muted-foreground font-normal italic")}
			data-testid={testId}
			data-kind={view.kind}
		>
			{rangeString(range, kind, locale, t)}
		</span>
	);
}

function Stat({ label, children, testId }: { label: string; children: React.ReactNode; testId: string }) {
	return (
		<div className="bg-muted/40 flex flex-col gap-0.5 rounded-sm border px-3 py-2" data-testid={testId}>
			<dt className="text-muted-foreground text-[11px]">{label}</dt>
			<dd className="text-sm font-semibold">{children}</dd>
		</div>
	);
}