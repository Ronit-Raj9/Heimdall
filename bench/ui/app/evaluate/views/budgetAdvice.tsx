import type { EstimateBenchmark, EstimateBudget, Range } from "@/lib/store/apis/evaluationsApi";
import { fw05 } from "./fw05I18n";
import { formatUsd } from "@/lib/money";

/**
 * The budget check of wizard step 5 (G4-01): which cap applies and whether the expected cost fits
 * it. When it does not, it says how many runs the cap would leave unstarted and offers the
 * suggested budget in one click. The numbers are the estimate's `budget` block and its
 * `est_cost_usd` range (the expected cost; `per_benchmark_usd` is only the cap).
 */

/** The estimate's expected cost of the whole evaluation. */
export type EstimateCost = Range & { known?: boolean };

function dollars(n: number): string {
	return formatUsd(n);
}

/** Money in the one UI format (`formatUsd`): a $1 budget reads "$1.00"; amounts under a cent keep their digits. */
export function usd(n: number): string {
	return formatUsd(n);
}

function runs(n: number): string {
	return `${n} ${n === 1 ? "run" : "runs"}`;
}

/** The warning line, or undefined when the cap covers the expected cost. */
export function budgetWarning(b: EstimateBudget): string | undefined {
	const cap = b.default ? `the default cap of ${dollars(b.per_benchmark_usd)} per benchmark` : `a budget of ${dollars(b.budget_usd)}`;
	switch (b.fit) {
		case "exceeds":
			return b.runs_skipped_low === b.runs_skipped_high
				? `With ${cap}, about ${runs(b.runs_skipped_high)} would not start.`
				: `With ${cap}, about ${b.runs_skipped_low} to ${runs(b.runs_skipped_high)} would not start.`;
		case "may_exceed":
			return `With ${cap}, up to ${runs(b.runs_skipped_high)} may not start if the cost is at the high end of the estimate.`;
		default:
			return undefined;
	}
}

/**
 * The one line that compares the budget with the expected cost (A1.text). It never prints the
 * per-benchmark cap as the expected cost, and says "Updating..." while a new estimate loads so an
 * old answer is not shown for a changed budget.
 */
export function budgetLine(b: EstimateBudget, cost: EstimateCost | undefined, isFetching: boolean): string {
	if (isFetching) return fw05("budget.updating");
	if (b.default) return fw05("budget.noBudget", { cap: usd(b.per_benchmark_usd) });
	const budget = usd(b.budget_usd);
	if (!cost || !cost.known) return fw05("budget.unknown", { budget });
	const range = { low: usd(cost.low), high: usd(cost.high) };
	// The server's fit splits the budget per benchmark (rounded down to cents), so a total that looks
	// covered can still leave runs unstarted: follow the fit, never say "covers" next to its warning.
	const short = b.fit === "exceeds" || b.fit === "may_exceed";
	return cost.high <= b.budget_usd && !short ? fw05("budget.covers", { budget, ...range }) : fw05("budget.mayExceed", { budget, ...range });
}

/**
 * The warning about one run's cost (fix wave 3, N190): the server starts a run only when its expected
 * cost fits a benchmark's share, so a share below one run's low cost starts nothing; with no known
 * cost the first run can pass the budget. `perRunLow` is the highest per-run low cost of a benchmark
 * (undefined when no cost is known) and `benchmarks` how many benchmarks share the budget.
 */
export function runWarning(b: EstimateBudget, perRunLow: number | undefined, benchmarks: number, _costKnown = false): string | undefined {
	if (b.default) return undefined;
	// An unknown price is said once, in the budget line (M3-28); no second "not known" line here.
	if (perRunLow === undefined || !(perRunLow > 0)) return undefined;
	if (b.per_benchmark_usd >= perRunLow) return undefined;
	return fw05("budget.belowOneRun", { run: usd(perRunLow), min: usd(Math.ceil(perRunLow * Math.max(1, benchmarks) * 100) / 100) });
}

/** The highest per-run low cost over the estimate's benchmarks; undefined when no benchmark's cost is known. */
export function perRunLowCost(per: readonly EstimateBenchmark[] | undefined): number | undefined {
	let out: number | undefined;
	for (const b of per ?? []) {
		if (b.skipped || !b.runs || !b.est_cost_usd?.known) continue;
		const run = b.est_cost_usd.low / b.runs;
		out = out === undefined ? run : Math.max(out, run);
	}
	return out;
}

export interface BudgetAdviceProps {
	budget: EstimateBudget;
	/** The estimate's `est_cost_usd`. */
	cost?: EstimateCost;
	/** True while the estimate refetches for a changed spec. */
	isFetching?: boolean;
	onSet(usd: number): void;
	/** The highest per-run low cost of a benchmark (`perRunLowCost`); undefined when unknown. */
	perRunLow?: number;
	/** The benchmarks that share the budget. */
	benchmarks?: number;
}

export function BudgetAdvice({ budget, cost, isFetching = false, onSet, perRunLow, benchmarks = 1 }: BudgetAdviceProps) {
	const line = budgetLine(budget, cost, isFetching);
	const oneRun = isFetching ? undefined : runWarning(budget, perRunLow, benchmarks, Boolean(cost?.known) || budget.fit !== "unknown");
	if (isFetching) {
		return (
			<span className="text-muted-foreground text-xs" role="status" data-testid="step5-budget-updating">
				{line}
			</span>
		);
	}
	const warn = budgetWarning(budget);
	const offer = budget.suggested_usd > budget.budget_usd ? budget.suggested_usd : 0;
	if (budget.fit === "fits" && !oneRun) {
		return (
			<span className="text-chart-success-ink text-xs" data-testid="step5-budget-fits">
				{line}
			</span>
		);
	}
	return (
		<div className="flex flex-col gap-1 text-xs" data-testid="step5-budget-advice">
			<span className="text-muted-foreground" data-testid="step5-budget-line">
				{line}
			</span>
			{warn && (
				<span className="text-chart-warning-ink" role="status" data-testid="step5-budget-warning">
					{warn} A benchmark stops starting runs when its share is spent; you can raise the budget and continue later.
				</span>
			)}
			{oneRun && (
				<span className="text-chart-warning-ink" role="status" data-testid="step5-budget-one-run">
					{oneRun}
				</span>
			)}
			{budget.note && <span className="text-muted-foreground">{budget.note[0].toUpperCase() + budget.note.slice(1)}.</span>}
			{offer > 0 && (
				<button
					type="button"
					className="text-primary w-fit hover:underline"
					onClick={() => onSet(offer)}
					data-testid="step5-budget-suggest"
				>
					Set budget to {dollars(offer)}
				</button>
			)}
		</div>
	);
}