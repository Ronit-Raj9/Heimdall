import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { getLocale } from "@/lib/i18n";
import { useGetExperimentParetoQuery, useGetLeaderboardParetoQuery } from "@/lib/store/apis/migrationApi";
import type { LeaderboardParetoParams, MigrationPair, MigrationReport, MigrationVerdict, Value } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { CostExplanationView } from "./costWaterfall";
import { costText, formatShare, observed } from "./pareto.utils";
import { ParetoChart } from "./paretoChart";

const ACTION_CLASS: Record<string, string> = {
	migrate: "border-[var(--color-chart-info)]",
	do_not_migrate: "border-[var(--color-chart-error)]",
	not_enough_evidence: "border-border",
};

/**
 * The model migration report (docs/model-migration.md): the recommendation per pair first, the
 * models with their Pareto position and the cost against success chart, then for each pair the
 * overall verdict, every stratum (Holm across the strata), behaviour, flipped tasks with links to
 * their run pairs, and why the cost moved. The recommendation is never stronger than the tests.
 */
export function MigrationReportView({ report }: { report: MigrationReport }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const unknown = t("migration.unknown");
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
			<ul className="flex flex-col gap-2" data-testid="migration-recommendations">
				{report.pairs.map((p) => (
					<li
						key={p.to.arm}
						className={`rounded-md border-y border-r border-l-4 p-3 text-sm ${ACTION_CLASS[p.recommendation.action] ?? ""}`}
						data-testid={`migration-recommendation-${p.to.arm}`}
						data-action={p.recommendation.action}
					>
						<strong>{t(`migration.action.${p.recommendation.action}`)}</strong>{" "}
						{t("migration.pairName", { from: p.from.model, to: p.to.model, arm: p.to.arm })}: {p.recommendation.reason}
					</li>
				))}
			</ul>
			<div className="overflow-x-auto">
				<table className="w-full min-w-[640px] text-sm" data-testid="migration-models">
					<thead>
						<tr className="text-muted-foreground text-left text-xs">
							{["arm", "model", "success", "passk", "costPerSuccess", "costPerRun", "tokens", "wall", "steps", "latency", "pareto"].map(
								(c) => (
									<th key={c} className="py-1 pr-3 font-medium" scope="col">
										{t(`migration.models.${c}` as "migration.models.arm")}
									</th>
								),
							)}
						</tr>
					</thead>
					<tbody>
						{report.models.map((m) => (
							<tr key={m.arm} className="border-t" data-testid={`migration-model-${m.arm}`}>
								<td className="py-1 pr-3 font-mono">{m.arm}</td>
								<td className="py-1 pr-3">{m.model}</td>
								<td className="py-1 pr-3 tabular-nums">{successText(m.success, locale, unknown)}</td>
								<td className="py-1 pr-3 tabular-nums">{formatShare(observed(m.reliability), locale) ?? unknown}</td>
								<td className="py-1 pr-3 font-mono tabular-nums">
									{costText(m.cost_per_success, locale) ?? naText(m.cost_per_success, unknown)}
								</td>
								<td className="py-1 pr-3 font-mono tabular-nums">{costText(m.cost_per_run, locale) ?? unknown}</td>
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
			</div>
			{report.pareto && <ParetoChart pareto={report.pareto} testId="migration-pareto" />}
			{report.pairs.map((p) => (
				<PairSection key={p.to.arm} pair={p} expId={report.exp_id} />
			))}
			<ul className="text-muted-foreground list-disc pl-5 text-xs" data-testid="migration-notes">
				{report.notes.map((n) => (
					<li key={n}>{n}</li>
				))}
			</ul>
		</div>
	);
}

function PairSection({ pair, expId }: { pair: MigrationPair; expId: string }) {
	const { t } = useTranslation("bench");
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
			<div className="overflow-x-auto">
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
			</div>
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
				<div className="overflow-x-auto">
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
				</div>
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
										to="/runs"
										search={{ pair: f.runs[0].pair_id }}
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
			{pair.cost && (
				<div className="flex flex-col gap-2" data-testid="migration-cost">
					<h3 className="text-foreground font-medium">{t("migration.cost.title")}</h3>
					<CostExplanationView explanation={pair.cost} />
				</div>
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
			<h3 className="text-foreground text-base font-semibold">{t("migration.pareto.title")}</h3>
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