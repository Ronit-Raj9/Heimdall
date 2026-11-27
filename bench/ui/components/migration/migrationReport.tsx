import { compareSearch } from "@/components/experiments/legacyRoutes";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { getLocale } from "@/lib/i18n";
import { useGetExperimentParetoQuery, useGetLeaderboardParetoQuery } from "@/lib/store/apis/migrationApi";
import type { LeaderboardParetoParams, MigrationPair, MigrationReport, MigrationVerdict, Value } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { Alert, type AlertTone } from "@/components/ui/alert";
import { useTranslation } from "react-i18next";
import { FadeScroll } from "./fadeScroll";
import "@/components/verdict/uf4aI18n";
import { costText, formatShare, observed } from "./pareto.utils";
import { ParetoChart } from "./paretoChart";
import { formatUsd2, observedNumber } from "@/components/verdict/armCost";

/** Average cost per run in the Result tab's format (formatUsd2), so the cost table and this table agree. */
export function meanCostText(v: Value | undefined, locale: string): string | null {
	const x = observedNumber(v);
	return x !== null ? formatUsd2(x, locale) : costText(v, locale);
}

/** The shared callout's tone per recommendation (X-98, M4-32): no side stripe. */
const ACTION_TONE: Record<string, AlertTone> = {
	migrate: "info",
	do_not_migrate: "danger",
	not_enough_evidence: "warning",
};

/** True when some model has a known pass^k, so the column is worth showing (M4-29). Pure. */
export function anyKnown(values: readonly (Value | undefined)[]): boolean {
	return values.some((v) => observed(v) !== null);
}

/**
 * The model migration report (docs/model-migration.md): the recommendation per pair first, the
 * models with their Pareto position and the cost against success chart, then for each pair the
 * overall verdict, every stratum (Holm across the strata), behaviour, flipped tasks with links to
 * their run pairs, and why the cost moved. The recommendation is never stronger than the tests.
 */
export function MigrationReportView({
	report,
	meanCost,
}: {
	report: MigrationReport;
	/** The average (mean) cost per run per arm from the cost explanation (M4-2); the server column is a median. */
	meanCost?: Record<string, Value | undefined>;
}) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("uf4a");
	const locale = getLocale();
	const unknown = t("migration.unknown");
	const showPassK = anyKnown(report.models.map((m) => m.reliability));
	const cols = [
		"arm",
		"model",
		"success",
		...(showPassK ? ["passk"] : []),
		"costPerSuccess",
		"costPerRun",
		"tokens",
		"wall",
		"steps",
		"latency",
		"pareto",
	];
	return (
		<div className="flex flex-col gap-6" data-testid="migration-report">
			{report.synthetic && (
				<p className="text-muted-foreground text-sm font-medium" data-testid="migration-synthetic">
					{t("migration.synthetic")}
				</p>
			)}
			<p className="text-muted-foreground text-sm">
				{t("migration.facts", {
					suite: report.suite,
					from: report.from.model,
					arm: report.from.arm,
					condition: report.condition,
					alpha: report.alpha,
				})}{" "}
				{report.policy ? t("migration.policy", { name: report.policy }) : t("migration.noPolicy")}
			</p>
			<p className="text-sm" data-testid="migration-lead">
				{t("migration.switchLead")}
			</p>
			<ul className="flex flex-col gap-2" data-testid="migration-recommendations">
				{report.pairs.map((p) => (
					<li key={p.to.arm} data-testid={`migration-recommendation-${p.to.arm}`} data-action={p.recommendation.action}>
						<Alert tone={ACTION_TONE[p.recommendation.action] ?? "info"}>
							<strong>{t(`migration.action.${p.recommendation.action}`)}</strong>{" "}
							{t("migration.pairName", { from: p.from.model, to: p.to.model, arm: p.to.arm })}: {p.recommendation.reason}
						</Alert>
					</li>
				))}
			</ul>
			{/* Q-159: a phone gets one card per model with every value; the wide table starts at sm. */}
			<ul className="flex flex-col gap-2 sm:hidden" data-testid="migration-models-cards">
				{report.models.map((m) => (
					<li key={m.arm} className="rounded-md border p-3 text-sm" data-testid={`migration-model-card-${m.arm}`}>
						<p className="font-medium">
							<span className="font-mono">{m.arm}</span> {m.model}
						</p>
						<dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
							<dt className="text-muted-foreground">{t("migration.models.success")}</dt>
							<dd className="tabular-nums">{successText(m.success, locale, unknown)}</dd>
							<dt className="text-muted-foreground">{t("migration.models.costPerSuccess")}</dt>
							<dd className="font-mono tabular-nums">{costText(m.cost_per_success, locale) ?? naText(m.cost_per_success, unknown)}</dd>
							<dt className="text-muted-foreground">{tu("cost.perRun")}</dt>
							<dd className="font-mono tabular-nums">{meanCostText(meanCost?.[m.arm], locale) ?? unknown}</dd>
							<dt className="text-muted-foreground">{t("migration.models.tokens")}</dt>
							<dd className="tabular-nums">{numText(m.tokens, unknown)}</dd>
							<dt className="text-muted-foreground">{t("migration.models.pareto")}</dt>
							<dd className="text-xs">{m.pareto}</dd>
						</dl>
					</li>
				))}
			</ul>
			<div className="hidden sm:block">
			<FadeScroll>
				<table className="w-full min-w-[640px] text-sm" data-testid="migration-models">
					<thead>
						<tr className="text-muted-foreground text-left text-xs">
							{cols.map((c) => (
								<th key={c} className="py-1 pr-3 font-medium" scope="col">
									{c === "costPerRun" ? tu("cost.perRun") : t(`migration.models.${c}` as "migration.models.arm")}
								</th>
							))}
						</tr>
					</thead>
					<tbody>
						{report.models.map((m) => (
							<tr key={m.arm} className="border-t" data-testid={`migration-model-${m.arm}`}>
								<td className="py-1 pr-3 font-mono">{m.arm}</td>
								<td className="py-1 pr-3">{m.model}</td>
								<td className="py-1 pr-3 tabular-nums">{successText(m.success, locale, unknown)}</td>
								{showPassK && <td className="py-1 pr-3 tabular-nums">{formatShare(observed(m.reliability), locale) ?? unknown}</td>}
								<td className="py-1 pr-3 font-mono tabular-nums">
									{costText(m.cost_per_success, locale) ?? naText(m.cost_per_success, unknown)}
								</td>
								{/* M4-2: the average per run, the median of the server only as a labelled second value. */}
								<td className="py-1 pr-3 tabular-nums" data-testid={`migration-cost-run-${m.arm}`}>
									<span className="font-mono">{meanCostText(meanCost?.[m.arm], locale) ?? unknown}</span>
									{costText(m.cost_per_run, locale) && (
										<span className="text-muted-foreground block text-xs">
											{tu("cost.median", { cost: costText(m.cost_per_run, locale) })}
										</span>
									)}
								</td>
								<td className="py-1 pr-3 tabular-nums">{numText(m.tokens, unknown)}</td>
								<td className="py-1 pr-3 tabular-nums">{numText(m.wall_time_s, unknown)}</td>
								<td className="py-1 pr-3 tabular-nums">{numText(m.steps, unknown)}</td>
								<td className="py-1 pr-3 tabular-nums">
									{observed(m.latency_ms_per_turn) !== null ? `${numText(m.latency_ms_per_turn, unknown)} ms` : unknown}
								</td>
								<td className="py-1 pr-3 text-xs">{m.pareto}</td>
							</tr>
						))}
					</tbody>
				</table>
			</FadeScroll>
			</div>
			{report.pareto && <ParetoChart pareto={report.pareto} testId="migration-pareto" />}
			{report.pairs.map((p) => (
				<PairSection key={p.to.arm} pair={p} expId={report.exp_id} />
			))}
			<ul className="text-muted-foreground list-disc pl-5 text-xs" data-testid="migration-notes">
				{reportNotes(report).map((n) => (
					<li key={n}>{n}</li>
				))}
			</ul>
		</div>
	);
}

/** The report notes that no pair already lists as a caveat, so a bullet is printed once (Q-178). */
export function reportNotes(report: Pick<MigrationReport, "notes" | "pairs">): string[] {
	const caveats = new Set(report.pairs.flatMap((p) => p.recommendation.caveats.map((c) => c.trim())));
	return report.notes.filter((n) => !caveats.has(n.trim()));
}

function PairSection({ pair, expId }: { pair: MigrationPair; expId: string }) {
	const { t } = useTranslation("bench");
	const { t: tf } = useTranslation("uf4a");
	const locale = getLocale();
	const unknown = t("migration.unknown");
	const bh = pair.behaviour;
	const change = (label: string, from: Value, to: Value, fmt: (v: Value) => string) => (
		<li key={label}>
			{label}: {fmt(from)} / {fmt(to)}
		</li>
	);
	const rate = (v: Value) => formatShare(observed(v), locale) ?? unknown;
	const num = (v: Value) => numText(v, unknown, 2);
	return (
		<section
			className="flex flex-col gap-4 rounded-md border p-4"
			data-testid={`migration-pair-${pair.to.arm}`}
			aria-label={t("migration.pairName", { from: pair.from.model, to: pair.to.model, arm: pair.to.arm })}
		>
			<h2 className="text-foreground text-lg font-semibold">
				{t("migration.pairName", { from: pair.from.model, to: pair.to.model, arm: pair.to.arm })}
			</h2>
			<p className="text-sm" data-testid="migration-overall">
				<strong>{t("migration.overall")}</strong> {verdictText(pair.overall, t)}
			</p>
			<ul className="text-muted-foreground list-disc pl-5 text-xs">
				{pair.recommendation.caveats.map((c) => (
					<li key={c}>{c}</li>
				))}
			</ul>
			{/* M4-29: the stratum table with its p values is for statisticians. */}
			<details className="group" data-testid="migration-strata-statistics">
				<summary className="text-primary w-fit cursor-pointer text-sm font-medium">{tf("verdict.forStatisticians")}</summary>
			<FadeScroll>
				<table className="w-full min-w-[560px] text-sm" data-testid="migration-strata">
					<caption className="text-muted-foreground pb-1 text-left text-xs">
						{t("migration.strata.caption", { count: pair.strata[0]?.family ?? 0, family: pair.strata[0]?.family ?? 0 })}
					</caption>
					<thead>
						<tr className="text-muted-foreground text-left text-xs">
							{["stratum", "tasks", "betterWorse", "meanDiff", "p", "pAdjusted", "result"].map((c) => (
								<th key={c} className="py-1 pr-3 font-medium" scope="col">
									{t(`migration.strata.${c}` as "migration.strata.stratum")}
								</th>
							))}
						</tr>
					</thead>
					<tbody>
						{pair.strata.map((s) => (
							<tr key={`${s.dimension}-${s.value}`} className="border-t" data-result={s.result}>
								<td className="py-1 pr-3">
									{t(`migration.dimension.${s.dimension}` as "migration.dimension.condition")}: {s.value}
								</td>
								<td className="py-1 pr-3 tabular-nums">{s.tasks}</td>
								<td className="py-1 pr-3 tabular-nums">
									{s.better} / {s.worse}
								</td>
								<td className="py-1 pr-3 tabular-nums">{s.mean_diff >= 0 ? `+${s.mean_diff.toFixed(3)}` : s.mean_diff.toFixed(3)}</td>
								<td className="py-1 pr-3 tabular-nums">{pText(s.p)}</td>
								<td className="py-1 pr-3 tabular-nums">{pText(s.p_adjusted)}</td>
								<td className="py-1 pr-3">{resultText(s, t)}</td>
							</tr>
						))}
					</tbody>
				</table>
			</FadeScroll>
			</details>
			<div className="flex flex-col gap-1 text-sm" data-testid="migration-behaviour">
				<h3 className="text-foreground font-medium">{t("migration.behaviour.title")}</h3>
				<ul className="text-muted-foreground list-disc pl-5">
					{change(t("migration.behaviour.falseCompletion"), bh.false_completion.from, bh.false_completion.to, rate)}
					{change(t("migration.behaviour.rejected"), bh.rejected_per_run.from, bh.rejected_per_run.to, num)}
					{change(t("migration.behaviour.refusals"), bh.refusals_per_run.from, bh.refusals_per_run.to, num)}
					{change(t("migration.behaviour.retries"), bh.retries_per_run.from, bh.retries_per_run.to, num)}
					{change(t("migration.behaviour.fallbacks"), bh.fallbacks_per_run.from, bh.fallbacks_per_run.to, num)}
					{bh.gates.map((g) => (
						<li key={g.gate} data-testid="migration-gate" data-status={g.status}>
							{t("migration.behaviour.gate", { gate: g.gate, from: g.from, to: g.to, status: t(`migration.gateStatus.${g.status}`) })}
						</li>
					))}
					{bh.new_clusters.map((c) => (
						<li key={c.signature} data-testid="migration-new-cluster">
							{t("migration.behaviour.newCluster", { title: c.title, count: c.count })}
						</li>
					))}
					{bh.fixed_clusters.map((c) => (
						<li key={c.signature} data-testid="migration-fixed-cluster">
							{t("migration.behaviour.fixedCluster", { title: c.title, count: c.count })}
						</li>
					))}
				</ul>
				<FadeScroll>
					<table className="w-full min-w-[480px] text-sm" data-testid="migration-conditions">
						<thead>
							<tr className="text-muted-foreground text-left text-xs">
								<th className="py-1 pr-3 font-medium" scope="col">
									{t("migration.conditions.condition")}
								</th>
								<th className="py-1 pr-3 font-medium" scope="col">
									{t("migration.conditions.runs")}
								</th>
								<th className="py-1 pr-3 font-medium" scope="col">
									{t("migration.conditions.success")}
								</th>
								<th className="py-1 pr-3 font-medium" scope="col">
									{t("migration.conditions.falseCompletion")}
								</th>
							</tr>
						</thead>
						<tbody>
							{bh.conditions.map((c) => (
								<tr key={c.condition} className="border-t">
									<td className="py-1 pr-3">{c.condition}</td>
									<td className="py-1 pr-3 tabular-nums">
										{c.from_runs} / {c.to_runs}
									</td>
									<td className="py-1 pr-3 tabular-nums">
										{rate(c.from_success)} / {rate(c.to_success)}
									</td>
									<td className="py-1 pr-3 tabular-nums">
										{rate(c.false_completion.from)} / {rate(c.false_completion.to)}
									</td>
								</tr>
							))}
						</tbody>
					</table>
				</FadeScroll>
			</div>
			{pair.flipped.length > 0 && (
				<div className="flex flex-col gap-1 text-sm" data-testid="migration-flipped">
					<h3 className="text-foreground font-medium">{t("migration.flipped.title", { count: pair.flipped.length })}</h3>
					<ul className="flex flex-col gap-0.5">
						{pair.flipped.map((f) => (
							<li key={`${f.condition}-${f.task}`} data-kind={f.kind}>
								<span className="font-mono">{f.task}</span> ({f.condition}) {t(`migration.flipKind.${f.kind}`)}:{" "}
								{formatShare(f.from_rate, locale)} / {formatShare(f.to_rate, locale)}{" "}
								{f.runs[0]?.pair_id && (
									<Link
										to="/compare"
										search={compareSearch(f.runs[0].pair_id) as never}
										className="text-primary underline-offset-4 hover:underline"
										data-testid="migration-flipped-link"
									>
										{t("migration.flipped.open")}
									</Link>
								)}
							</li>
						))}
					</ul>
				</div>
			)}
			{/* M4-31: the Cost tab holds the full breakdown; the switch decision links it instead of repeating it. */}
			{pair.cost && (
				<p className="text-sm" data-testid="migration-cost">
					<Link
						to="/experiments/$expId"
						params={{ expId }}
						search={{ tab: "cost" } as never}
						className="text-primary font-medium hover:underline"
						data-testid="migration-cost-link"
					>
						{tf("migration.seeCost")}
					</Link>
				</p>
			)}
			<span className="sr-only">{expId}</span>
		</section>
	);
}

type T = ReturnType<typeof useTranslation<"bench">>["t"];

function verdictText(v: MigrationVerdict, t: T): string {
	const needed = v.result === "cannot_tell" && v.tasks_needed > 0 ? ` ${t("migration.tasksNeeded", { count: v.tasks_needed })}` : "";
	return `${v.reason}.${needed}`;
}

function resultText(v: MigrationVerdict, t: T): string {
	const base = t(`migration.result.${v.result}`);
	return v.result === "cannot_tell" && v.tasks_needed > 0 ? `${base} (${t("migration.tasksNeeded", { count: v.tasks_needed })})` : base;
}

function pText(p: number | null): string {
	if (p === null) return "-";
	return p < 0.1 ? p.toPrecision(2) : p.toFixed(2);
}

function successText(v: Value, locale: string, unknown: string): string {
	const x = formatShare(observed(v), locale);
	if (x === null) return unknown;
	return v.bounds ? `${x} [${formatShare(v.bounds[0], locale)}, ${formatShare(v.bounds[1], locale)}]` : x;
}

function numText(v: Value, unknown: string, digits = 0): string {
	const x = observed(v);
	if (x !== null) return x.toFixed(digits);
	return v.state === "not_applicable" ? "n/a" : unknown;
}

function naText(v: Value, unknown: string): string {
	if (v.state === "not_applicable") return v.reason ? `n/a (${v.reason})` : "n/a";
	return unknown;
}

/** The Pareto chart of one experiment's arms, for the matrix page. */
export function ExperimentParetoPanel({ expId, condition }: { expId: string; condition?: string }) {
	const { t } = useTranslation("bench");
	const query = useGetExperimentParetoQuery({ expId, condition });
	return (
		<section className="flex flex-col gap-3 rounded-md border p-4" data-testid="experiment-pareto-panel">
			<h2 className="text-foreground text-base font-semibold">{t("migration.pareto.title")}</h2>
			{query.isLoading ? (
				<BlockSkeleton rows={4} testId="experiment-pareto-loading" />
			) : query.error || !query.data ? (
				<LoadError
					title={t("migration.pareto.loadError")}
					error={query.error}
					onRetry={() => void query.refetch()}
					testId="experiment-pareto-error"
				/>
			) : (
				<ParetoChart pareto={query.data} testId="experiment-pareto" />
			)}
		</section>
	);
}

/** The Pareto chart of the board's configs, for the leaderboard page; same query as the board. */
export function LeaderboardParetoPanel({ params }: { params: LeaderboardParetoParams }) {
	const { t } = useTranslation("bench");
	const query = useGetLeaderboardParetoQuery(params);
	return (
		<section className="flex flex-col gap-3 rounded-md border p-4" data-testid="leaderboard-pareto-panel">
			<h3 className="text-foreground text-base font-semibold">{t("migration.pareto.title")}</h3>
			{query.isLoading ? (
				<BlockSkeleton rows={4} testId="leaderboard-pareto-loading" />
			) : query.error || !query.data ? (
				<LoadError
					title={t("migration.pareto.loadError")}
					error={query.error}
					onRetry={() => void query.refetch()}
					testId="leaderboard-pareto-error"
				/>
			) : (
				<ParetoChart pareto={query.data} testId="leaderboard-pareto" />
			)}
		</section>
	);
}