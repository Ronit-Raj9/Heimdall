import { render } from "@/lib/testing/render";
import type { EstimateBudget } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it } from "vitest";
import { BudgetAdvice, budgetLine, perRunLowCost, runWarning } from "./budgetAdvice";

const budget = (over: Partial<EstimateBudget> = {}): EstimateBudget => ({
	budget_usd: 1,
	per_benchmark_usd: 1,
	default: false,
	default_per_benchmark_usd: 5,
	suggested_usd: 1,
	max_usd: 100,
	fit: "fits",
	runs_skipped_low: 0,
	runs_skipped_high: 0,
	...over,
});

const known = { low: 0.06, high: 0.29, known: true };

describe("budgetLine", () => {
	it("says the typed budget covers the expected cost, with the cost range, not the cap", () => {
		const line = budgetLine(budget(), known, false);
		expect(line).toBe("Your $1.00 budget covers the expected cost ($0.06 to $0.29).");
		expect(line).not.toContain("$5");
	});

	it("warns when the high end of the expected cost is over the budget", () => {
		expect(budgetLine(budget({ budget_usd: 0.2, per_benchmark_usd: 0.2, fit: "may_exceed" }), known, false)).toBe(
			"The expected cost ($0.06 to $0.29) may go over your $0.20 budget. Runs stop when the budget is used up.",
		);
	});

	it("says when the cost is not known", () => {
		expect(budgetLine(budget({ fit: "unknown" }), { low: 0, high: 0, known: false }, false)).toBe(
			"The cost is not known yet. Runs stop when the $1.00 budget is used up.",
		);
		expect(budgetLine(budget(), undefined, false)).toBe("The cost is not known yet. Runs stop when the $1.00 budget is used up.");
	});

	it("names the per-benchmark cap when no budget is typed", () => {
		expect(budgetLine(budget({ default: true, budget_usd: 10, per_benchmark_usd: 5 }), known, false)).toBe(
			"No budget set: each benchmark may spend up to $5.00.",
		);
	});

	it("fit exceeds never says covers (N92)", () => {
		const cheap = { low: 0.001, high: 0.004, known: true };
		for (const fit of ["exceeds", "may_exceed"] as const) {
			const line = budgetLine(budget({ budget_usd: 0.01, per_benchmark_usd: 0, fit }), cheap, false);
			expect(line).not.toContain("covers");
			expect(line).toContain("may go over your $0.01 budget");
		}
	});

	it("says Updating... while a new estimate loads", () => {
		expect(budgetLine(budget(), known, true)).toBe("Updating...");
	});
});

describe("BudgetAdvice", () => {
	it("renders the cost line and never the cap as the expected cost", () => {
		const html = render(<BudgetAdvice budget={budget()} cost={known} isFetching={false} onSet={() => undefined} />);
		expect(html).toContain("Your $1.00 budget covers the expected cost ($0.06 to $0.29).");
		expect(html).not.toContain("($1 per benchmark)");
	});

	it("hides the old advice while fetching", () => {
		const html = render(
			<BudgetAdvice
				budget={budget({ fit: "exceeds", runs_skipped_low: 2, runs_skipped_high: 3, suggested_usd: 4 })}
				cost={known}
				isFetching
				onSet={() => undefined}
			/>,
		);
		expect(html).toContain("Updating...");
		expect(html).not.toContain("would not start");
		expect(html).not.toContain("Set budget to");
	});
});

describe("one run's cost (fix wave 3, N190)", () => {
	it("warns when the budget is less than one run costs, so no run would start", () => {
		const small = budget({ budget_usd: 0.01, per_benchmark_usd: 0.01, suggested_usd: 5 });
		const run = perRunLowCost([{ benchmark: "bfcl", runs: 5, est_cost_usd: { low: 0.1, high: 0.2, known: true } }]);
		expect(run).toBeCloseTo(0.02);
		expect(runWarning(small, run, 1)).toBe(
			"This budget is less than one run costs (about $0.02 per run), so no run would start. Set at least $0.02.",
		);
		const html = render(<BudgetAdvice budget={small} cost={known} onSet={() => undefined} perRunLow={run} benchmarks={1} />);
		expect(html).toContain('data-testid="step5-budget-one-run"');
		expect(html).toContain("Set budget to");
	});

	it("says an unknown run cost splits the budget evenly across the runs, and nothing when a run fits", () => {
		expect(runWarning(budget(), undefined, 1)).toBe(
			"The cost of a run is not known yet, so the budget is split evenly across the runs. A run can cost more than its share, so the total can pass the budget a little before it stops.",
		);
		expect(runWarning(budget(), 0.02, 1)).toBeUndefined();
		expect(runWarning(budget({ default: true }), undefined, 1)).toBeUndefined();
	});
});