import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { CanarySummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { formatUSD, isCanaryState, sortCanaries, stateVariant, targetLabel } from "./canary.utils";

/** One canary state as a badge; a state this UI does not know shows as the server sent it. */
export function CanaryStateBadge({ state, testId }: { state: string; testId?: string }) {
	const { t } = useTranslation("bench");
	return (
		<Badge variant={stateVariant(state)} data-state={state} data-testid={testId}>
			{isCanaryState(state) ? t(`canaries.state.${state}`) : state}
		</Badge>
	);
}

export interface CanaryListProps {
	canaries: readonly CanarySummary[];
	selected?: string | null;
	onSelect: (name: string) => void;
}

/**
 * Every canary: its state first (drifted, then watching, invalid, stale), what it watches, the
 * baseline it compares with, when it looks next and what it spent today against its daily cap.
 */
export function CanaryList({ canaries, selected, onSelect }: CanaryListProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<Table stickyColumns={1} aria-label={t("canaries.list.label")} data-testid="canary-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("canaries.list.columns.name")}</TableHead>
					<TableHead>{t("canaries.list.columns.state")}</TableHead>
					<TableHead>{t("canaries.list.columns.targets")}</TableHead>
					<TableHead>{t("canaries.list.columns.baseline")}</TableHead>
					<TableHead className="text-right">{t("canaries.list.columns.looks")}</TableHead>
					<TableHead>{t("canaries.list.columns.next")}</TableHead>
					<TableHead className="text-right">{t("canaries.list.columns.spend")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{sortCanaries(canaries).map((c) => {
					const isSelected = c.name === selected;
					return (
						<TableRow
							key={c.name}
							className={cn("cursor-pointer align-top", isSelected && "bg-accent")}
							aria-selected={isSelected}
							onClick={() => onSelect(c.name)}
							data-testid={`canary-row-${c.name}`}
						>
							<TableCell className="max-w-72">
								<button
									type="button"
									className="focus-visible:ring-ring/50 rounded-sm text-left text-sm font-medium underline-offset-2 hover:underline focus-visible:ring-[3px] focus-visible:outline-none"
									onClick={(e) => {
										e.stopPropagation();
										onSelect(c.name);
									}}
								>
									{c.name}
								</button>
								{c.description && <p className="text-muted-foreground line-clamp-2 text-xs">{c.description}</p>}
								{c.smoke && (
									<Badge variant="outline" className="mt-1" data-testid={`canary-smoke-${c.name}`}>
										{t("canaries.smoke")}
									</Badge>
								)}
							</TableCell>
							<TableCell className="max-w-80">
								<CanaryStateBadge state={c.state} testId={`canary-state-${c.name}`} />
								<p className="text-muted-foreground mt-1 line-clamp-3 text-xs" title={c.reason}>
									{c.reason}
								</p>
							</TableCell>
							<TableCell className="font-mono text-xs">
								{c.targets.map((tg) => (
									<div key={targetLabel(tg)}>{targetLabel(tg)}</div>
								))}
							</TableCell>
							<TableCell className="font-mono text-xs">{c.baseline ? c.baseline.exp_id : t("canaries.list.noBaseline")}</TableCell>
							<TableCell className="text-right tabular-nums">{c.looks}</TableCell>
							<TableCell className="text-xs">
								{c.next ? (formatDateTime(c.next, locale) ?? c.next) : t("canaries.list.notScheduled")}
							</TableCell>
							<TableCell className="text-right text-xs tabular-nums" data-testid={`canary-spend-${c.name}`}>
								{c.per_day_usd > 0
									? t("canaries.list.spendOfCap", { spent: formatUSD(c.spent_today_usd, locale), cap: formatUSD(c.per_day_usd, locale) })
									: t("canaries.list.noCap")}
							</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}