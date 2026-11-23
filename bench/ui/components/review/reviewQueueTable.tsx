import { OutcomeBadge } from "@/components/runs/outcomeBadge";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { ReviewQueueItem } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { ReasonBadge } from "./reasonBadge";

export interface ReviewQueueTableProps {
	items: readonly ReviewQueueItem[];
	selectedRunId?: string | null;
	onSelect: (item: ReviewQueueItem) => void;
}

/**
 * The review queue: one row per run a person should look at, with why, where it sits in the
 * experiment, its sealed outcome and the outcome in effect under grade source reviewed. A row
 * opens the run.
 */
export function ReviewQueueTable({ items, selectedRunId, onSelect }: ReviewQueueTableProps) {
	const { t } = useTranslation("bench");
	return (
		<Table stickyColumns={1} data-testid="review-queue-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("review.queue.columns.run")}</TableHead>
					<TableHead>{t("review.queue.columns.task")}</TableHead>
					<TableHead>{t("review.queue.columns.arm")}</TableHead>
					<TableHead>{t("review.queue.columns.condition")}</TableHead>
					<TableHead className="text-right">{t("review.queue.columns.trial")}</TableHead>
					<TableHead>{t("review.queue.columns.reasons")}</TableHead>
					<TableHead>{t("review.queue.columns.sealed")}</TableHead>
					<TableHead>{t("review.queue.columns.current")}</TableHead>
					<TableHead>{t("review.queue.columns.decision")}</TableHead>
					<TableHead className="text-right">{t("review.queue.columns.reviews")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{items.map((item) => {
					const selected = item.run_id === selectedRunId;
					const changed = item.outcome !== item.sealed_outcome;
					return (
						<TableRow
							key={item.run_id}
							className={cn("cursor-pointer", selected && "bg-accent")}
							aria-selected={selected}
							onClick={() => onSelect(item)}
							data-testid={`review-queue-row-${item.run_id}`}
						>
							<TableCell>
								<button
									type="button"
									className="focus-visible:ring-ring/50 rounded-sm font-mono text-xs underline-offset-2 hover:underline focus-visible:ring-[3px] focus-visible:outline-none"
									aria-label={t("review.queue.select", { id: item.run_id })}
									onClick={(e) => {
										e.stopPropagation();
										onSelect(item);
									}}
									data-testid={`review-queue-open-${item.run_id}`}
								>
									{item.run_id}
								</button>
							</TableCell>
							<TableCell className="font-mono text-xs">{item.task}</TableCell>
							<TableCell className="font-mono text-xs">{item.arm}</TableCell>
							<TableCell className="font-mono text-xs">{item.condition}</TableCell>
							<TableCell className="text-right tabular-nums">{item.trial}</TableCell>
							<TableCell>
								<div className="flex flex-wrap gap-1">
									{item.reasons.map((reason) => (
										<ReasonBadge key={reason} reason={reason} detail={(item as typeof item & { thrown_out_by?: string[] }).thrown_out_by} testId={`review-queue-reason-${item.run_id}-${reason}`} />
									))}
								</div>
							</TableCell>
							<TableCell>
								<OutcomeBadge outcome={item.sealed_outcome} testId={`review-queue-sealed-${item.run_id}`} />
							</TableCell>
							<TableCell>
								<OutcomeBadge
									outcome={item.outcome}
									className={cn(changed && "ring-1 ring-amber-500/70")}
									testId={`review-queue-current-${item.run_id}`}
								/>
							</TableCell>
							<TableCell data-testid={`review-queue-decision-${item.run_id}`}>
								{item.decision ? (
									<Badge variant="outline">{t(`review.decision.${item.decision}`)}</Badge>
								) : (
									<span className="text-muted-foreground text-xs">{t("review.queue.noDecision")}</span>
								)}
							</TableCell>
							<TableCell className="text-right tabular-nums">{item.reviews}</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}