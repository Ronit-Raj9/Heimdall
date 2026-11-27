import { compatFor, compatibilityMap, groupBenchmarks, incompatibleReason, isGpuBenchmark } from "@/app/evaluate/views/step3Benchmarks";
import { EmptyState } from "@/components/shell/emptyState";
import { suiteDisplayDescription, suiteDisplayName } from "@/components/suites/suiteDisplay";
import type { AgentCompatibility } from "@/lib/store/apis/evaluationsApi";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import "@/components/reportcard/reportCardI18n";
import "@/lib/types/evaluateI18n";

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

const NEEDS: Record<string, string> = {
	browser: "benchmarks.needsBrowser",
	voice: "benchmarks.needsVoice",
	desktop: "benchmarks.needsDesktop",
	gpu: "benchmarks.needsGpu",
};

/**
 * A refusal in plain words (M3-33): "agent codex cannot run benchmark webarena-pro: needs browser" becomes "Needs a
 * browser agent"; other reasons lose the "agent ... cannot run benchmark ...:" prefix and start with a capital.
 */
export function plainReason(raw: string, t: (key: string, opts?: Record<string, unknown>) => string): string {
	const rest = raw.replace(/^agent .* cannot run benchmark [^:]*:\s*/i, "").trim();
	const need = /^needs?\s+(?:an?\s+)?([a-z]+)(?:\s+worker)?\.?$/i.exec(rest);
	if (need) {
		const cap = need[1].toLowerCase();
		return NEEDS[cap] ? t(NEEDS[cap]) : t("benchmarks.needsCapability", { capability: cap });
	}
	return rest || raw;
}

/** A description worth showing: "Imported by ..." boilerplate is hidden (M3-34). */
function shownDescription(text: string): string {
	return /^imported by\b/i.test(text.trim()) ? "" : text;
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
	const { t } = useTranslation("evaluate");
	const compat = compatibilityMap(compatibility);
	const chosen = new Set(selected);
	const [query, setQuery] = useState("");
	const q = query.trim().toLowerCase();
	const matches = (s: ChecklistSuite) => !q || s.id.toLowerCase().includes(q) || suiteDisplayName(s.id).toLowerCase().includes(q);
	const { groups: allGroups } = groupBenchmarks(suites);
	// Rows the agent cannot run leave their groups for one closed "Not available" group at the end (M3-33).
	const blocked = (s: ChecklistSuite) => Boolean(incompatibleReason(compatFor(compat, s.id)));
	const groups = allGroups
		.map((g) => ({ ...g, suites: g.suites.filter((s) => !blocked(s) && matches(s)) }))
		.filter((g) => g.suites.length > 0);
	const unavailable = allGroups.flatMap((g) => g.suites.filter((s) => blocked(s) && matches(s)));
	// Only the group holding a pick (or the first one) starts open.
	const openGroup = groups.find((g) => g.suites.some((s) => chosen.has(s.id)))?.capability ?? groups[0]?.capability;

	const row = (s: ChecklistSuite) => {
		const c = compatFor(compat, s.id);
		const reason = incompatibleReason(c);
		const disabled = Boolean(reason) || Boolean(checking);
		const on = chosen.has(s.id) && !disabled;
		const n = s.admitted_count ?? 0;
		const note =
			(reason ? plainReason(reason, t as never) : "") ||
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
						"flex min-h-11 w-full items-center gap-3 rounded-md px-2 py-2 text-sm sm:min-h-9",
						disabled ? "text-muted-foreground cursor-not-allowed" : "hover:bg-muted/60 cursor-pointer",
					)}
				>
					<input
						type="checkbox"
						className="accent-primary size-4 shrink-0"
						checked={on}
						disabled={disabled}
						onChange={() => onToggle(s.id)}
					/>
					<span className="min-w-0 flex-1">
						{/* Phones: the ready count goes under the name, so nothing runs off the side (M5-41). */}
						<span className="flex flex-col gap-0.5 sm:flex-row sm:items-baseline sm:justify-between sm:gap-3">
							<span className="truncate font-medium">{suiteDisplayName(s.id)}</span>
							<span className={cn("shrink-0 text-xs tabular-nums", n ? "text-muted-foreground" : "text-warning-ink")}>
								{n ? `${plural(n, "ready task")}` : "No ready tasks"}
							</span>
						</span>
						<span className="text-muted-foreground line-clamp-2 block text-sm" title={note || undefined}>
							{note ? note : shownDescription(suiteDisplayDescription(s))}
						</span>
						{extra?.(s.id)}
					</span>
				</label>
			</li>
		);
	};

	if (allGroups.length === 0) {
		if (loading) {
			// Skeleton rows while the list loads, not an empty box (M3-35).
			return (
				<div className="flex flex-col gap-2" aria-busy="true" data-testid={`${testIdPrefix}-loading`}>
					<span className="sr-only">{t("benchmarks.loading")}</span>
					{Array.from({ length: 8 }, (_, i) => (
						<div key={i} className="bg-muted h-11 animate-pulse rounded-md" />
					))}
				</div>
			);
		}
		// The shared empty block with the way to the benchmark status (M3-36, M3-46).
		return (
			<EmptyState
				title={t("benchmarks.noneReadyTitle")}
				body={t("benchmarks.noneReadyBody")}
				action={{ label: t("benchmarks.seeStatus"), to: "/suites" }}
				compact
				testId={`${testIdPrefix}-none-ready`}
			/>
		);
	}
	return (
		<div className="flex flex-col gap-3">
			<input
				type="search"
				value={query}
				onChange={(e) => setQuery(e.target.value)}
				placeholder={t("benchmarks.search")}
				aria-label={t("benchmarks.search")}
				className="border-input bg-background h-10 w-full max-w-md rounded-md border px-3 text-sm"
				data-testid={`${testIdPrefix}-benchmark-search`}
			/>
			{groups.map((g) => {
				const picked = g.suites.filter((s) => chosen.has(s.id)).length;
				const free = g.suites.filter((s) => !chosen.has(s.id) && !checking);
				return (
					<details
						key={g.capability}
						open={Boolean(q) || g.capability === openGroup}
						className="rounded-lg border"
						data-testid={`${testIdPrefix}-group-${g.capability}`}
					>
						<summary className="hover:bg-muted/50 flex min-h-11 cursor-pointer items-center gap-3 rounded-lg px-3 text-sm font-medium">
							<span className="flex-1">{g.label}</span>
							<span className="text-muted-foreground text-xs font-normal tabular-nums">
								{t("benchmarks.selected", { n: picked, m: g.suites.length })}
							</span>
						</summary>
						<div className="flex flex-col gap-1 border-t px-1 py-2">
							{free.length > 0 && (
								<button
									type="button"
									className="text-primary w-fit px-2 text-xs font-medium hover:underline"
									onClick={() => free.forEach((s) => onToggle(s.id))}
								>
									{t("benchmarks.selectAll")}
								</button>
							)}
							<ul className="flex flex-col">{g.suites.map(row)}</ul>
						</div>
					</details>
				);
			})}
			{unavailable.length > 0 && (
				<details className="rounded-lg border" data-testid={`${testIdPrefix}-group-unavailable`}>
					<summary className="text-muted-foreground hover:bg-muted/50 flex min-h-11 cursor-pointer items-center rounded-lg px-3 text-sm font-medium">
						{t("benchmarks.notAvailable", { agent: compatibility?.agent ?? "this agent", count: unavailable.length })}
					</summary>
					<ul className="flex flex-col border-t px-1 py-2">{unavailable.map(row)}</ul>
				</details>
			)}
		</div>
	);
}
