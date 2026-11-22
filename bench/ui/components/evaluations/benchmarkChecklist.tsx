import { compatFor, compatibilityMap, groupBenchmarks, incompatibleReason, isGpuBenchmark } from "@/app/evaluate/views/step3Benchmarks";
import { suiteDisplayDescription, suiteDisplayName } from "@/components/suites/suiteDisplay";
import type { AgentCompatibility } from "@/lib/store/apis/evaluationsApi";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import type { ReactNode } from "react";

/** The suite fields the checklist reads. */
export type ChecklistSuite = Pick<SuiteSummary, "id" | "description" | "admitted_count" | "builtin" | "source" | "track" | "environment">;

export interface BenchmarkChecklistProps {
	suites: readonly ChecklistSuite[];
	/** The chosen agent's compatibility; undefined reads every row as runnable. */
	compatibility?: AgentCompatibility;
	/** The agent's compatibility is still loading: rows wait instead of reading as runnable. */
	checking?: boolean;
	selected: readonly string[];
	onToggle: (id: string) => void;
	/** The suite list is still loading. */
	loading?: boolean;
	/** Prefix of the row and group test ids (`<prefix>-benchmark-<id>`, `<prefix>-group-<capability>`). */
	testIdPrefix?: string;
	/** An extra line under a row (step 3 shows published reference scores there). */
	extra?: (id: string) => ReactNode;
}

function plural(n: number, word: string): string {
	return `${n} ${word}${n === 1 ? "" : "s"}`;
}

/**
 * The benchmark checklist the evaluate wizard (step 3) and Failure tests share: benchmarks with at
 * least one ready task, grouped by capability, each with its display name and ready task count. A
 * benchmark the chosen agent cannot run is greyed out with the reason.
 */
export function BenchmarkChecklist({
	suites,
	compatibility,
	checking,
	selected,
	onToggle,
	loading,
	testIdPrefix = "benchmarks",
	extra,
}: BenchmarkChecklistProps) {
	const compat = compatibilityMap(compatibility);
	const chosen = new Set(selected);
	const { groups } = groupBenchmarks(suites);

	const row = (s: ChecklistSuite) => {
		const c = compatFor(compat, s.id);
		const reason = incompatibleReason(c);
		const disabled = Boolean(reason) || Boolean(checking);
		const on = chosen.has(s.id) && !disabled;
		const n = s.admitted_count ?? 0;
		const note =
			reason ||
			(checking ? "Checking whether your agent can run it..." : "") ||
			(isGpuBenchmark(s.id) ? "Needs a GPU worker" : "") ||
			(c?.ok ? (c.note ?? "") : "");
		return (
			<li key={s.id}>
				<label
					data-testid={`${testIdPrefix}-benchmark-${s.id}`}
					data-disabled={disabled || undefined}
					title={s.id}
					className={cn(
						"flex items-start gap-3 rounded-md px-2 py-1.5 text-sm",
						disabled ? "cursor-not-allowed opacity-50" : "hover:bg-muted/60 cursor-pointer",
					)}
				>
					<input
						type="checkbox"
						className="accent-primary mt-0.5 size-3.5"
						checked={on}
						disabled={disabled}
						onChange={() => onToggle(s.id)}
					/>
					<span className="min-w-0 flex-1">
						<span className="flex items-baseline justify-between gap-3">
							<span className="truncate font-medium">{suiteDisplayName(s.id)}</span>
							<span className={cn("shrink-0 text-xs tabular-nums", n ? "text-muted-foreground" : "text-amber-600 dark:text-amber-400")}>
								{n ? `${plural(n, "ready task")}` : "No ready tasks"}
							</span>
						</span>
						<span className="text-muted-foreground block truncate text-xs" title={note || undefined}>
							{note ? note : suiteDisplayDescription(s)}
						</span>
						{extra?.(s.id)}
					</span>
				</label>
			</li>
		);
	};

	if (groups.length === 0) {
		return (
			<p className="text-muted-foreground text-sm">
				{loading
					? "Loading benchmarks..."
					: "No benchmark has ready tasks yet. Benchmarks that are not ready yet are listed on the Benchmarks page."}
			</p>
		);
	}
	return (
		<div className="flex flex-col gap-4">
			{groups.map((g) => (
				<div key={g.capability} data-testid={`${testIdPrefix}-group-${g.capability}`}>
					<div className="text-muted-foreground mb-1 px-2 text-xs font-medium tracking-wide uppercase">{g.label}</div>
					<ul className="flex flex-col">{g.suites.map(row)}</ul>
				</div>
			))}
		</div>
	);
}