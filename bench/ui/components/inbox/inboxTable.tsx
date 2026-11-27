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
	/** An item panel sits beside the table (2xl): keep only the columns that pick a row, so the table fits its narrower box. */
	compact?: boolean;
}

/**
 * Where each optional column shows (Failures table: the request text must stay in its own cell and the table
 * must fit at 1440 and 1920 without sideways scroll). Columns that only add detail appear from lg or 2xl, and
 * hide while the item panel sits beside the table. Pure, exported for tests.
 */
export function inboxColumnClass(column: "flagged" | "firstSeen" | "suggested" | "trace" | "task", compact: boolean): string {
	// The panel goes beside the table only from 2xl; below that it sits under the table, which keeps its columns.
	switch (column) {
		case "flagged":
			return compact ? "hidden lg:table-cell 2xl:hidden" : "hidden lg:table-cell";
		case "task":
			return compact ? "hidden" : "hidden 2xl:table-cell";
		default:
			return compact ? "hidden" : "hidden 2xl:table-cell";
	}
}

/**
 * The inbox: one row per failure signature, with why it was flagged, how often and when it was
 * seen, its state, what it would become, a link to the original trace when the source has one,
 * and for an accepted item the regression task it became and how that task is doing.
 */
export function InboxTable({ items, selectedId, onSelect, compact = false }: InboxTableProps) {
	const col = (c: Parameters<typeof inboxColumnClass>[0]) => inboxColumnClass(c, compact);
	const { t } = useTranslation("bench");
	const kindLabel = useKindLabel();
	return (
		<>
			{/* Below sm: one stacked row per item (request, then source, times seen and state), so no column is cut at 390 px (V7-2). */}
			<ul className="flex min-w-0 flex-col divide-y rounded-md border sm:hidden" aria-label={t("inbox.table.label")} data-testid="inbox-list-narrow">
				{items.map((item) => (
					<li key={item.id} className={cn("min-w-0", item.id === selectedId && "bg-accent")}>
						<button
							type="button"
							className="focus-visible:ring-ring/50 flex w-full min-w-0 flex-col gap-1 p-3 text-left focus-visible:ring-[3px] focus-visible:outline-none"
							aria-label={t("inbox.table.open", { id: item.id })}
							aria-current={item.id === selectedId ? "true" : undefined}
							onClick={() => onSelect(item)}
							data-testid={`inbox-narrow-${item.id}`}
						>
							<span className="line-clamp-2 text-sm font-medium [overflow-wrap:anywhere]">{item.title}</span>
							<span className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm">
								<span className="font-mono [overflow-wrap:anywhere]">{item.source}</span>
								<span className="tabular-nums">
									{t("inbox.table.columns.occurrences")}: {item.occurrences}
								</span>
								<InboxStateBadge state={item.state} testId={`inbox-narrow-state-${item.id}`} />
							</span>
						</button>
					</li>
				))}
			</ul>
			<div className="hidden min-w-0 sm:block">
				<Table aria-label={t("inbox.table.label")} data-testid="inbox-table">
					<TableHeader>
						<TableRow>
							<TableHead className="w-full">{t("inbox.table.columns.title")}</TableHead>
							<TableHead>{t("inbox.table.columns.source")}</TableHead>
							<TableHead className={col("flagged")}>{t("inbox.table.columns.flagged")}</TableHead>
							<TableHead className="text-right">{t("inbox.table.columns.occurrences")}</TableHead>
							<TableHead className={col("firstSeen")}>{t("inbox.table.columns.firstSeen")}</TableHead>
							<TableHead>{t("inbox.table.columns.lastSeen")}</TableHead>
							<TableHead>{t("inbox.table.columns.state")}</TableHead>
							<TableHead className={col("suggested")}>{t("inbox.table.columns.suggested")}</TableHead>
							<TableHead className={col("trace")}>{t("inbox.table.columns.trace")}</TableHead>
							<TableHead className={col("task")}>{t("inbox.table.columns.task")}</TableHead>
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
									<TableCell className="w-full min-w-48 overflow-hidden whitespace-normal" data-testid={`inbox-title-cell-${item.id}`}>
										<button
											type="button"
											className="focus-visible:ring-ring/50 line-clamp-2 block w-full min-w-0 rounded-sm text-left text-sm font-medium [overflow-wrap:anywhere] underline-offset-2 hover:underline focus-visible:ring-[3px] focus-visible:outline-none"
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
										<span className="text-muted-foreground line-clamp-1 block font-mono text-xs [overflow-wrap:anywhere]" title={item.id}>
											{item.id}
										</span>
									</TableCell>
									<TableCell>
										<div className="flex flex-col">
											<span className="font-mono text-xs [overflow-wrap:anywhere]">{item.source}</span>
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
									<TableCell className={cn("max-w-64 min-w-56 whitespace-normal", col("flagged"))} data-testid={`inbox-flagged-${item.id}`}>
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
									<TableCell className={cn("text-xs whitespace-nowrap", col("firstSeen"))}>{formatInboxTime(item.first_seen)}</TableCell>
									<TableCell className="text-xs whitespace-nowrap">{formatInboxTime(item.last_seen)}</TableCell>
									<TableCell>
										<InboxStateBadge state={item.state} testId={`inbox-state-badge-${item.id}`} />
									</TableCell>
									<TableCell className={cn("text-xs", col("suggested"))} data-testid={`inbox-suggested-${item.id}`}>
										<div className="flex flex-col">
											<span>{kindLabel(item.suggested_kind)}</span>
											<span className="text-muted-foreground font-mono">{item.suggested_suite ?? t("inbox.table.noSuite")}</span>
										</div>
									</TableCell>
									<TableCell className={col("trace")} onClick={(e) => e.stopPropagation()}>
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
									<TableCell className={cn("text-xs", col("task"))} data-testid={`inbox-task-${item.id}`}>
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
			</div>
		</>
	);
}