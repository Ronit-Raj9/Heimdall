import "./w4b06I18n";

import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { getLocale } from "@/lib/i18n";
import { useGetCostExplainQuery } from "@/lib/store/apis/migrationApi";
import type { CostApart, CostArm, CostExplanation, CostWaterfall as Waterfall, Value } from "@/lib/types/bench";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { FadeScroll } from "./fadeScroll";
import "@/components/verdict/uf4aI18n";
import { costText, formatShare, observed, signedUsd, waterfallBars } from "./pareto.utils";
import { formatUsd2, observedNumber } from "@/components/verdict/armCost";

/** The measuring costs (judge, user simulator) to list: rows that are zero or unknown for both setups are left out (M4-24). Pure. */
export function measuredApart(apart: readonly CostApart[]): CostApart[] {
	const nothing = (v: CostApart["a"]) => (observed(v) ?? 0) === 0 && !v?.bounds;
	return apart.filter((x) => !(nothing(x.a) && nothing(x.b)));
}

/**
 * Why arm B cost more than arm A (docs/cost-explain.md): the two arms side by side, then the
 * difference in cost per run and per verified success as a waterfall of parts that sum to the total.
 * An unknown part is a hatched range from its bounds, never a zero; measuring costs (judge, user
 * simulator) are listed apart; a silent cache break names its likely cause.
 */
export function CostExplanationView({ explanation }: { explanation: CostExplanation }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const { a, b } = explanation;
	const { t: tw } = useTranslation("w4b06");
	const { t: tu } = useTranslation("uf4a");
	const unknown = t("migration.unknown");
	// N200: the configured model, then the provider's own name for it on a second line only when it differs.
	const modelCell = (arm: CostArm): ReactNode => {
		const served = arm.served_models.filter((m) => m !== arm.model);
		if (served.length === 0) return arm.model;
		return (
			<>
				{arm.model}
				<span className="text-muted-foreground block text-xs" data-testid="cost-answered-as">
					{tw("cost.answeredAs", { served: served.join(", ") })}
				</span>
			</>
		);
	};
	const rows: [string, (arm: CostArm) => ReactNode][] = [
		[tw("cost.model"), modelCell],
		[t("migration.cost.row.runs"), (arm) => t("migration.cost.runsValue", { runs: arm.runs, successes: arm.successes })],
		// The mean, the one cost per run of every tab, with two significant digits (M4-2).
		[
			tu("cost.perRun"),
			(arm) => {
				const mean = observedNumber(arm.cost_per_run);
				return mean !== null ? formatUsd2(mean, locale) : valueReason(arm.cost_per_run, unknown);
			},
		],
		[tu("cost.perSuccess"), (arm) => costText(arm.cost_per_success, locale) ?? valueReason(arm.cost_per_success, unknown)],
		[
			tu("cost.cacheNeutral"),
			(arm) => costText(arm.cache_neutral_per_success, locale) ?? valueReason(arm.cache_neutral_per_success, unknown),
		],
		[t("migration.cost.row.cacheRead"), (arm) => formatShare(observed(arm.cache_read_share), locale) ?? unknown],
		[t("migration.cost.row.turns"), (arm) => arm.turns_per_run.toFixed(1)],
		[
			t("migration.cost.row.tokensPerTurn"),
			(arm) => `${Math.round(arm.input_tokens_per_turn)} / ${Math.round(arm.output_tokens_per_turn)}`,
		],
		// Q-157: the attempts row is left out when no setup retried, fell back or was rejected.
		...(attemptsOf(a) + attemptsOf(b) > 0
			? [
					[
						t("migration.cost.row.attempts"),
						(arm: CostArm) => `${arm.retries_per_run.toFixed(2)} / ${arm.fallbacks_per_run.toFixed(2)} / ${arm.rejected_per_run.toFixed(2)}`,
					] as [string, (arm: CostArm) => ReactNode],
				]
			: []),
		// M4-24: a row that is 0 for both setups says nothing and is left out.
		...(a.unpriced_rows + a.unattributed_rows + b.unpriced_rows + b.unattributed_rows > 0
			? [
					[t("migration.cost.row.unpriced"), (arm: CostArm) => `${arm.unpriced_rows} / ${arm.unattributed_rows}`] as [
						string,
						(arm: CostArm) => ReactNode,
					],
				]
			: []),
	];
	return (
		<section className="flex flex-col gap-4" data-testid="cost-explanation" aria-label={t("migration.cost.title")}>
			{explanation.synthetic && (
				<p className="text-muted-foreground text-sm font-medium" data-testid="cost-synthetic">
					{t("migration.synthetic")}
				</p>
			)}
			<FadeScroll pinStart>
				{/* Q-156: no fixed width on a phone, so setup B stays in view at 390 px; the wide layout keeps its minimum. */}
				<table className="w-full text-sm sm:min-w-[420px]" data-testid="cost-arms">
					<thead>
						<tr className="text-muted-foreground text-left text-xs">
							<th className="py-1 pr-3 font-medium" scope="col" />
							<th className="py-1 pr-3 font-medium" scope="col">
								{setupHead(a)}
							</th>
							<th className="py-1 pr-3 font-medium" scope="col">
								{setupHead(b)}
							</th>
						</tr>
					</thead>
					<tbody>
						{rows.map(([label, cell]) => (
							<tr key={label} className="border-t">
								<th className="text-muted-foreground bg-card sticky left-0 py-1 pr-3 text-left font-normal" scope="row">
									{label}
								</th>
								<td className="py-1 pr-3 font-mono tabular-nums">{cell(a)}</td>
								<td className="py-1 pr-3 font-mono tabular-nums">{cell(b)}</td>
							</tr>
						))}
					</tbody>
				</table>
			</FadeScroll>
			<WaterfallView title={t("migration.cost.perRun")} waterfall={explanation.per_run} testId="cost-waterfall-run" />
			<WaterfallView title={t("migration.cost.perSuccess")} waterfall={explanation.per_success} testId="cost-waterfall-success" />
			{/* M4-24: the Shapley note is for statisticians. */}
			<details className="group" data-testid="cost-statistics">
				<summary className="text-primary w-fit cursor-pointer text-sm font-medium">{tu("verdict.forStatisticians")}</summary>
				<p className="text-muted-foreground mt-2 text-sm">{t("migration.cost.shapley")}</p>
			</details>
			{measuredApart(explanation.apart).length === 0 ? (
				<p className="text-muted-foreground text-sm" data-testid="cost-no-measuring">
					{t("migration.cost.noMeasuring")}
				</p>
			) : (
			<div className="flex flex-col gap-1 text-sm" data-testid="cost-apart">
				<h4 className="text-foreground font-medium">{t("migration.cost.apart")}</h4>
				<ul className="text-muted-foreground list-disc pl-5">
					{measuredApart(explanation.apart).map((x) => (
						<li key={x.id}>
							{x.label}: {costText(x.a, locale) ?? valueReason(x.a, unknown)} / {costText(x.b, locale) ?? valueReason(x.b, unknown)}
						</li>
					))}
				</ul>
			</div>
			)}
			<div className="flex flex-col gap-1 text-sm" data-testid="cost-cache">
				<h4 className="text-foreground font-medium">{t("migration.cost.cache")}</h4>
				{explanation.cache.length === 0 ? (
					<p className="text-muted-foreground">{t("migration.cost.noCacheBreak")}</p>
				) : (
					explanation.cache.map((f, i) => (
						<div key={`${f.kind}-${f.arm}-${i}`} className="flex flex-col gap-1" data-testid="cost-cache-finding">
							<p className="text-foreground">
								{t("migration.cost.cacheBreak", {
									arm: f.arm,
									after: formatShare(f.share_after, locale) ?? "",
									before: formatShare(f.share_before, locale) ?? "",
									baseline: f.baseline,
								})}{" "}
								<strong>{t("migration.cost.likelyCause")}</strong> {f.cause}
							</p>
							<ul className="text-muted-foreground list-disc pl-5 text-xs">
								{f.evidence.map((e) => (
									<li key={e}>{e}</li>
								))}
							</ul>
						</div>
					))
				)}
			</div>
			{explanation.notes.length > 0 && (
				<ul className="text-muted-foreground list-disc pl-5 text-xs" data-testid="cost-notes">
					{explanation.notes.map((n) => (
						<li key={n}>{n}</li>
					))}
				</ul>
			)}
		</section>
	);
}

/** Retries, fallbacks and rejected calls per run, summed: 0 means the attempts row says nothing. */
export function attemptsOf(arm: Pick<CostArm, "retries_per_run" | "fallbacks_per_run" | "rejected_per_run">): number {
	return Math.abs(arm.retries_per_run) + Math.abs(arm.fallbacks_per_run) + Math.abs(arm.rejected_per_run);
}

/** "A (gpt-5.6-terra)": the setup letter and its model, the name every tab uses (Q-139); the label when no model. */
export function setupHead(arm: Pick<CostArm, "arm" | "label" | "model">): string {
	return arm.model ? `${arm.arm} (${arm.model})` : `${arm.arm} (${arm.label})`;
}

/** Waterfall parts worth a line: a part that is exactly $0 says nothing (Q-157, "Wasted attempts $0"). */
export function shownParts<T extends { usd: Value }>(parts: readonly T[]): T[] {
	return parts.filter((p) => observed(p.usd) !== 0);
}

function valueReason(v: Value, unknown: string): string {
	if (v.state === "not_applicable") return v.reason ? `n/a (${v.reason})` : "n/a";
	return v.reason ? `${unknown} (${v.reason})` : unknown;
}

/** One waterfall: a bar per part on a shared track, the amount in text beside it, then the total. */
function WaterfallView({ title, waterfall, testId }: { title: string; waterfall: Waterfall; testId: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const unknown = t("migration.unknown");
	if (waterfall.reason) {
		return (
			<div className="flex flex-col gap-1 text-sm" data-testid={testId}>
				<h4 className="text-foreground font-medium">{title}</h4>
				<p className="text-muted-foreground">{t("migration.cost.notApplicable", { reason: waterfall.reason })}</p>
			</div>
		);
	}
	const bars = waterfallBars(shownParts(waterfall.parts));
	const total = observed(waterfall.total);
	return (
		<div className="flex flex-col gap-2 text-sm" data-testid={testId}>
			<h4 className="text-foreground font-medium">{title}</h4>
			<p className="text-muted-foreground text-xs">
				{t("migration.cost.fromTo", {
					from: costText(waterfall.from, locale) ?? unknown,
					to: costText(waterfall.to, locale) ?? unknown,
					total: total !== null ? signedUsd(total, locale) : (costText(waterfall.total, locale) ?? unknown),
				})}
			</p>
			<ol className="flex flex-col gap-1">
				{bars.map((bar) => {
					const x = observed(bar.part.usd);
					const lo = Math.min(bar.start, bar.end);
					const hi = Math.max(bar.start, bar.end);
					const up = bar.end >= bar.start;
					return (
						<li
							key={bar.part.id}
							className="grid grid-cols-[minmax(8rem,14rem)_1fr_6rem] items-center gap-2"
							data-testid={`${testId}-part-${bar.part.id}`}
							title={bar.part.detail}
						>
							<span className="text-muted-foreground truncate text-xs">{bar.part.label}</span>
							<span className="bg-muted relative h-3 rounded-sm" aria-hidden="true">
								<span className="bg-border absolute top-0 bottom-0 w-px" style={{ left: "50%" }} />
								<span
									className="absolute top-0.5 bottom-0.5 rounded-sm"
									data-known={bar.known ? "true" : "false"}
									style={{
										left: `${50 + lo * 50}%`,
										width: `${Math.max((hi - lo) * 50, x === 0 ? 0 : 0.5)}%`,
										background: !bar.known
											? "repeating-linear-gradient(45deg, var(--color-chart-neutral) 0 3px, transparent 3px 6px)"
											: up
												? "var(--color-chart-error)"
												: "var(--color-chart-info)",
									}}
								/>
							</span>
							<span className="text-right font-mono text-xs tabular-nums">
								{x !== null ? signedUsd(x, locale) : (costText(bar.part.usd, locale) ?? unknown)}
							</span>
						</li>
					);
				})}
				<li className="grid grid-cols-[minmax(8rem,14rem)_1fr_6rem] items-center gap-2 border-t pt-1" data-testid={`${testId}-total`}>
					<span className="text-foreground text-xs font-medium">{t("migration.cost.total")}</span>
					<span aria-hidden="true" />
					<span className="text-right font-mono text-xs font-medium tabular-nums">
						{total !== null ? signedUsd(total, locale) : (costText(waterfall.total, locale) ?? unknown)}
					</span>
				</li>
			</ol>
			{/* M4-26: the note on how the parts add up reads as a caption, not inside a narrow table cell. */}
			<p className="text-muted-foreground mt-2 text-xs">{t("migration.cost.legend")}</p>
		</div>
	);
}

/** The cost explanation panel for the verdict and matrix pages: fetches and renders B against A. */
export function CostExplainPanel({ expId, a, b, condition }: { expId: string; a?: string; b?: string; condition?: string }) {
	const { t } = useTranslation("bench");
	const query = useGetCostExplainQuery({ expId, a, b, condition });
	return (
		<section className="flex flex-col gap-3 rounded-md border p-4" data-testid="cost-explain-panel">
			<h2 className="text-foreground text-base font-semibold">{t("migration.cost.title")}</h2>
			{query.isLoading ? (
				<BlockSkeleton rows={4} testId="cost-explain-loading" />
			) : query.error || !query.data ? (
				<LoadError
					title={t("migration.cost.loadError")}
					error={query.error}
					onRetry={() => void query.refetch()}
					testId="cost-explain-error"
				/>
			) : (
				<CostExplanationView explanation={query.data} />
			)}
		</section>
	);
}