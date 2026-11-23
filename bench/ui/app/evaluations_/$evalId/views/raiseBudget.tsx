import { formatCost } from "@/components/leaderboard/agentBoard.utils";
import { Button } from "@/components/ui/button";
import { type Evaluation, type EvaluationChild, useRaiseEvaluationBudgetMutation } from "@/lib/store/apis/evaluationsApi";
import { useState } from "react";

/**
 * Budget on the evaluation page (G4-01): each benchmark's spend against its cap, the benchmarks the
 * cap stopped with the runs they never started, and "Raise budget and continue", which calls
 * POST /evaluations/{id}/raise-budget. Nothing that already ran runs again.
 */

function dollars(n: number): string {
	return `$${n.toFixed(2)}`;
}

/** "$1.84 of $5.00 spent" for a child with a cap; undefined otherwise. */
export function childSpendText(c: EvaluationChild): string | undefined {
	if (!c.budget_usd) return undefined;
	return `${dollars(c.spent_usd ?? 0)} of ${dollars(c.budget_usd)} spent`;
}

/** A child with the expected cost of the run the budget did not start (fix wave 3, N190); absent on an older server. */
type ChildWithNextRun = EvaluationChild & { next_run_usd?: number };

/**
 * "Stopped by the budget: spent $0.0094 of $0.01; the next run was expected to cost about $0.0030. 4 runs did
 * not start." for a child the cap stopped (fix wave 3, N190). Money keeps 4 decimals under one cent.
 */
export function childStoppedText(c: EvaluationChild): string | undefined {
	if (!c.budget_stopped) return undefined;
	const n = c.not_started ?? 0;
	const next = (c as ChildWithNextRun).next_run_usd;
	const spent = `Stopped by the budget: spent ${formatCost(c.spent_usd ?? 0)} of ${formatCost(c.budget_usd ?? 0)}`;
	const est = next && next > 0 ? `; the next run was expected to cost about ${formatCost(next)}` : "";
	return `${spent}${est}. ${n} ${n === 1 ? "run" : "runs"} did not start.`;
}

const TERMINAL = new Set(["done", "failed", "cancelled", "skipped"]);

/** The children a raise applies to and the current total of their caps. */
export function raiseTargets(ev: Evaluation): { stopped: EvaluationChild[]; current: number; notStarted: number } {
	const kids = (ev.children ?? []).filter((c) => c.experiment_id && c.status !== "skipped");
	const stopped = kids.filter((c) => c.budget_stopped);
	const open = kids.filter((c) => c.budget_stopped || !TERMINAL.has(c.status));
	return {
		stopped,
		current: open.reduce((s, c) => s + (c.budget_usd ?? 0), 0),
		notStarted: stopped.reduce((s, c) => s + (c.not_started ?? 0), 0),
	};
}

/** A first suggestion for the new total: each stopped benchmark's cap doubled, whole dollars. */
export function suggestedRaise(ev: Evaluation): number {
	const { stopped, current } = raiseTargets(ev);
	const extra = stopped.reduce((s, c) => s + Math.max(c.budget_usd ?? 0, 1), 0);
	return Math.ceil(current + extra);
}

function errorText(e: unknown): string {
	const data = (e as { data?: { error?: { message?: string } | string } } | undefined)?.data;
	if (typeof data?.error === "string") return data.error;
	return data?.error?.message ?? "Could not raise the budget.";
}

export function RaiseBudgetPanel({ evaluation }: { evaluation: Evaluation }) {
	const { stopped, current, notStarted } = raiseTargets(evaluation);
	const [text, setText] = useState(() => String(suggestedRaise(evaluation)));
	const [raise, state] = useRaiseEvaluationBudgetMutation();
	if (stopped.length === 0) return null;
	const value = Number(text);
	const valid = Number.isFinite(value) && value > current && value <= 1000;
	return (
		<section
			className="border-chart-warning/40 bg-chart-warning/8 flex flex-col gap-3 rounded-xl border p-4 text-sm"
			data-testid="evaluation-budget-stopped"
		>
			<div className="flex flex-col gap-1">
				<span className="font-medium">
					The budget stopped {stopped.length} {stopped.length === 1 ? "benchmark" : "benchmarks"}: {notStarted}{" "}
					{notStarted === 1 ? "run" : "runs"} not started.
				</span>
				<span className="text-muted-foreground">
					The report card only counts the runs that finished. Raise the budget to run the rest; finished runs are kept and never run again.
				</span>
			</div>
			<div className="flex flex-wrap items-end gap-3">
				<label className="flex flex-col gap-1">
					<span className="text-xs font-medium">New budget for the evaluation (USD, now {dollars(current)})</span>
					<input
						type="number"
						min={0}
						step="1"
						inputMode="decimal"
						className="border-input bg-background h-8 w-32 rounded-md border px-2 text-sm"
						value={text}
						onChange={(e) => setText(e.target.value)}
						data-testid="evaluation-raise-budget-input"
					/>
				</label>
				<Button
					size="sm"
					disabled={!valid || state.isLoading}
					onClick={() => void raise({ id: evaluation.id, budget_usd: value })}
					data-testid="evaluation-raise-budget"
				>
					{state.isLoading ? "Continuing..." : "Raise budget and continue"}
				</Button>
			</div>
			{!valid && text.trim() !== "" && (
				<span className="text-muted-foreground text-xs">Type more than {dollars(current)} and at most $1000.</span>
			)}
			{state.error ? (
				<span className="text-chart-error-ink text-xs" role="alert" data-testid="evaluation-raise-budget-error">
					{errorText(state.error)}
				</span>
			) : null}
		</section>
	);
}