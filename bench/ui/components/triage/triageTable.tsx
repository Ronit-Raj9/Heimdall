import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatCount, formatSignedPp } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { Ratio, TriageGroup } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { share, shareDiffPoints } from "./triage.utils";

export interface TriageTableProps {
	groups: readonly TriageGroup[];
	/** Arm order; the difference column is arms[1] minus arms[0]. */
	arms: readonly string[];
	/** The pattern whose runs are shown, or null. */
	selected: string | null;
	onSelect: (pattern: string | null) => void;
}

/**
 * Patterns (rows) by arm (columns): runs showing the pattern over runs where it was observed, the
 * share, unknowns apart, and a descriptive difference between the first two arms. No test is run.
 */
export function TriageTable({ groups, arms, selected, onSelect }: TriageTableProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const [armA, armB] = arms;
	const showDiff = arms.length >= 2;

	return (
		<Table stickyColumns={1} data-testid="triage-table" aria-label={t("triage.table.label")}>
			<TableHeader>
				<TableRow>
					<TableHead>{t("triage.table.pattern")}</TableHead>
					{arms.map((arm) => (
						<TableHead key={arm} className="text-right" data-testid={`triage-head-${arm}`}>
							{t("triage.table.arm", { arm })}
						</TableHead>
					))}
					{showDiff && (
						<TableHead className="text-right" title={t("triage.table.diffHint")} data-testid="triage-head-diff">
							{t("triage.table.diff", { a: armA, b: armB })}
						</TableHead>
					)}
					<TableHead />
				</TableRow>
			</TableHeader>
			<TableBody>
				{groups.map((group) => {
					const open = selected === group.pattern;
					const diff = showDiff ? shareDiffPoints(group, armA, armB) : null;
					return (
						<TableRow key={group.pattern} data-testid={`triage-row-${group.pattern}`} data-state={open ? "selected" : undefined}>
							<TableCell className="max-w-md align-top whitespace-normal">
								<div className="flex flex-col gap-0.5">
									<span className="text-foreground font-medium">{group.label}</span>
									<span className="text-muted-foreground font-mono text-[11px]">{group.pattern}</span>
									<span className="text-muted-foreground text-xs">{group.description}</span>
								</div>
							</TableCell>
							{arms.map((arm) => (
								<TableCell key={arm} className="text-right align-top" data-testid={`triage-cell-${group.pattern}-${arm}`}>
									<RatioCell ratio={group.per_arm[arm]} locale={locale} />
								</TableCell>
							))}
							{showDiff && (
								<TableCell className="text-right align-top tabular-nums" data-testid={`triage-diff-${group.pattern}`}>
									{diff === null ? (
										<span className="text-muted-foreground text-xs">{t("triage.table.noDiff")}</span>
									) : (
										<span className={cn("font-medium", diff > 0 && "text-chart-warning-ink")}>
											{t("triage.table.points", { value: formatSignedPp(diff / 100, locale) })}
										</span>
									)}
								</TableCell>
							)}
							<TableCell className="text-right align-top">
								<button
									type="button"
									aria-expanded={open}
									aria-controls="triage-drilldown"
									aria-label={t(open ? "triage.table.hideAria" : "triage.table.showAria", { pattern: group.label })}
									onClick={() => onSelect(open ? null : group.pattern)}
									data-testid={`triage-show-${group.pattern}`}
									className={cn(
										"focus-visible:ring-ring/50 inline-flex cursor-pointer items-center gap-1 rounded-sm border px-2 py-1 text-xs font-medium transition-colors focus-visible:ring-[3px]",
										open ? "border-primary bg-primary/10" : "hover:bg-accent",
									)}
								>
									{open ? (
										<ChevronDown className="size-3.5" aria-hidden="true" />
									) : (
										<ChevronRight className="size-3.5" aria-hidden="true" />
									)}
									{t("triage.table.show")}
								</button>
							</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}

/** "3/24", the share, and unknowns apart. An arm with nothing observed says so, hatched, never 0%. */
function RatioCell({ ratio, locale }: { ratio: Ratio | undefined; locale: string }) {
	const { t } = useTranslation("bench");
	const value = share(ratio);
	const unknown = ratio?.unknown ?? 0;
	return (
		<div className="flex flex-col items-end gap-0.5">
			{value === null ? (
				<span className="bench-hatched text-muted-foreground rounded-sm border border-dashed px-1.5 text-xs">
					{t("triage.table.noObserved")}
				</span>
			) : (
				<>
					<span className="font-semibold tabular-nums">
						{formatCount(ratio!.num, locale)}/{formatCount(ratio!.den, locale)}
					</span>
					<span className="text-muted-foreground text-xs tabular-nums">
						{new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 1 }).format(value)}
					</span>
				</>
			)}
			{unknown > 0 && (
				<span className="bench-hatched text-muted-foreground rounded-sm border border-dashed px-1 text-[11px]">
					{t("triage.table.unknown", { count: unknown })}
				</span>
			)}
		</div>
	);
}