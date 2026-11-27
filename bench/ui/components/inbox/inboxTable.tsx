import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { InboxItem } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { formatInboxTime, InboxStateBadge, ReleaseBadge, TraceSourceLink, useKindLabel } from "./inboxBadges";

/** Flagged reasons shown per row before "+n more". */
const FLAGGED_SHOWN = 2;

export interface InboxTableProps {
	items: readonly InboxItem[];
	selectedId?: string | null;
	onSelect: (item: InboxItem) => void;
}

/**
 * The inbox: one row per failure signature, with why it was flagged, how often and when it was
 * seen, its state, what it would become, a link to the original trace when the source has one,
 * and for an accepted item the regression task it became and how that task is doing.
 */
export function InboxTable({ items, selectedId, onSelect }: InboxTableProps) {
	const { t } = useTranslation("bench");
	const kindLabel = useKindLabel();
	return (
		<Table stickyColumns={1} aria-label={t("inbox.table.label")} data-testid="inbox-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("inbox.table.columns.title")}</TableHead>
					<TableHead>{t("inbox.table.columns.source")}</TableHead>
					<TableHead>{t("inbox.table.columns.flagged")}</TableHead>
					<TableHead className="text-right">{t("inbox.table.columns.occurrences")}</TableHead>
					<TableHead>{t("inbox.table.columns.firstSeen")}</TableHead>
					<TableHead>{t("inbox.table.columns.lastSeen")}</TableHead>
					<TableHead>{t("inbox.table.columns.state")}</TableHead>
					<TableHead>{t("inbox.table.columns.suggested")}</TableHead>
					<TableHead>{t("inbox.table.columns.trace")}</TableHead>
					<TableHead>{t("inbox.table.columns.task")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{items.map((item) => {
					const selected = item.id === selectedId;
					const flagged = item.flagged ?? [];
					const hidden = flagged.length - FLAGGED_SHOWN;
					return (
						<TableRow
							key={item.id}
							className={cn("cursor-pointer align-top", selected && "bg-accent")}
							aria-selected={selected}
							onClick={() => onSelect(item)}
							data-testid={`inbox-row-${item.id}`}
						>
							<TableCell className="max-w-80">
								<button
									type="button"
									className="focus-visible:ring-ring/50 line-clamp-2 rounded-sm text-left text-sm font-medium break-words underline-offset-2 hover:underline focus-visible:ring-[3px] focus-visible:outline-none"
									aria-label={t("inbox.table.open", { id: item.id })}
									title={item.title}
									onClick={(e) => {
										e.stopPropagation();
										onSelect(item);
									}}
									data-testid={`inbox-open-${item.id}`}
								>
									{item.title}
								</button>
								<span className="text-muted-foreground block font-mono text-xs">{item.id}</span>
							</TableCell>
							<TableCell>
								<div className="flex flex-col">
									<span className="font-mono text-xs">{item.source}</span>
									{/* The kind only when it says more than the name ("webhook webhook", M7-12); then the model the session used. */}
									{item.source_kind && item.source_kind !== item.source && (
										<span className="text-muted-foreground text-xs">{item.source_kind}</span>
									)}
									{item.model && (
										<span className="text-muted-foreground font-mono text-xs" data-testid={`inbox-model-${item.id}`}>
											{item.model}
										</span>
									)}
								</div>
							</TableCell>
							<TableCell className="max-w-72" data-testid={`inbox-flagged-${item.id}`}>
								<ul className="flex flex-col gap-0.5 text-xs">
									{flagged.slice(0, FLAGGED_SHOWN).map((reason, i) => (
										<li key={`${i}-${reason}`} className="line-clamp-2 break-words" title={reason}>
											{reason}
										</li>
									))}
									{hidden > 0 && <li className="text-muted-foreground">{t("inbox.table.moreFlagged", { count: hidden })}</li>}
								</ul>
							</TableCell>
							<TableCell className="text-right tabular-nums" data-testid={`inbox-occurrences-${item.id}`}>
								{item.occurrences}
							</TableCell>
							<TableCell className="text-xs whitespace-nowrap">{formatInboxTime(item.first_seen)}</TableCell>
							<TableCell className="text-xs whitespace-nowrap">{formatInboxTime(item.last_seen)}</TableCell>
							<TableCell>
								<InboxStateBadge state={item.state} testId={`inbox-state-badge-${item.id}`} />
							</TableCell>
							<TableCell className="text-xs" data-testid={`inbox-suggested-${item.id}`}>
								<div className="flex flex-col">
									<span>{kindLabel(item.suggested_kind)}</span>
									<span className="text-muted-foreground font-mono">{item.suggested_suite ?? t("inbox.table.noSuite")}</span>
								</div>
							</TableCell>
							<TableCell onClick={(e) => e.stopPropagation()}>
								{item.link ? (
									<TraceSourceLink
										link={item.link}
										traceId={item.trace_id}
										source={item.source}
										label={t("inbox.table.openTrace")}
										testId={`inbox-trace-link-${item.id}`}
									/>
								) : (
									<span className="text-muted-foreground text-xs">{t("inbox.table.noLink")}</span>
								)}
							</TableCell>
							<TableCell className="text-xs" data-testid={`inbox-task-${item.id}`}>
								{item.task ? (
									<div className="flex flex-col items-start gap-1">
										<span className="font-mono">
											{t("inbox.table.acceptedAs", {
												suite: item.task.suite,
												task: item.task.id,
											})}
										</span>
										<ReleaseBadge release={item.release} testId={`inbox-release-${item.id}`} />
										{(item.recurrences ?? 0) > 0 && (
											<span className="text-chart-warning-ink" data-testid={`inbox-recurrences-${item.id}`}>
												{t("inbox.table.recurrences", {
													count: item.recurrences ?? 0,
												})}
											</span>
										)}
									</div>
								) : (
									<span className="text-muted-foreground">{t("inbox.table.noTask")}</span>
								)}
							</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}