import type { InboxList } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { INBOX_FILTERS, inboxCount, type InboxFilter } from "./inbox.utils";

export interface InboxStateFilterProps {
	counts: InboxList["counts"] | undefined;
	value: InboxFilter;
	onChange: (filter: InboxFilter) => void;
}

/**
 * Counts by state as filter chips: All, then each state. Counts are over every item, whatever
 * the filter, so a chip says what picking it would show.
 */
export function InboxStateFilter({ counts, value, onChange }: InboxStateFilterProps) {
	const { t } = useTranslation("bench");
	return (
		<div role="group" aria-label={t("inbox.filters.label")} className="flex flex-wrap gap-2" data-testid="inbox-state-filter">
			{INBOX_FILTERS.map((filter) => {
				const active = filter === value;
				const count = inboxCount(counts, filter);
				return (
					<button
						key={filter}
						type="button"
						aria-pressed={active}
						onClick={() => onChange(filter)}
						className={cn(
							"focus-visible:ring-ring/50 inline-flex h-8 items-center gap-2 rounded-md border px-3 text-sm font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
							active ? "border-primary bg-primary/10 text-primary" : "bg-card text-foreground hover:bg-accent",
						)}
						data-testid={`inbox-state-${filter}`}
					>
						<span>{t(`inbox.state.${filter}`)}</span>
						<span
							className={cn("rounded-sm px-1.5 text-xs tabular-nums", active ? "bg-primary/15" : "bg-muted text-muted-foreground")}
							data-testid={`inbox-count-${filter}`}
						>
							{count}
						</span>
					</button>
				);
			})}
		</div>
	);
}