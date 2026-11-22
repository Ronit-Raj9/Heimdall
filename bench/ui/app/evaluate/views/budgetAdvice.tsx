import type { EstimateBudget } from "@/lib/store/apis/evaluationsApi";

/**
 * The budget check of wizard step 5 (G4-01): which cap applies and whether the expected cost fits
 * it. When it does not, it says how many runs the cap would leave unstarted and offers the
 * suggested budget in one click. The numbers are the estimate's `budget` block.
 */

function dollars(n: number): string {
	return Number.isInteger(n) ? `$${n}` : `$${n.toFixed(2)}`;
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

export function BudgetAdvice({ budget, onSet }: { budget: EstimateBudget; onSet(usd: number): void }) {
	const warn = budgetWarning(budget);
	const offer = budget.suggested_usd > budget.budget_usd ? budget.suggested_usd : 0;
	if (budget.fit === "fits") {
		return (
			<span className="text-chart-success-ink text-xs" data-testid="step5-budget-fits">
				This budget covers the expected cost ({dollars(budget.per_benchmark_usd)} per benchmark).
			</span>
		);
	}
	return (
		<div className="flex flex-col gap-1 text-xs" data-testid="step5-budget-advice">
			{warn && (
				<span className="text-chart-warning-ink" role="status" data-testid="step5-budget-warning">
					{warn} A benchmark stops starting runs when its share is spent; you can raise the budget and continue later.
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
