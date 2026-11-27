import "@/app/inbox/views/w443I18n";

import type { Finding } from "@/lib/store/apis/findingsApi";
import { FindingArchiveMenu, FindingArchivedPill } from "@/components/findings/findingArchiveMenu";
import { ARCHIVED_ROW_CLASS } from "@/components/ui/showArchivedChip";
import { isArchived } from "@/lib/store/apis/archiveApi";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

/** The badge colour of each finding state; reopened uses the warning colour. */
export function stateClass(state: Finding["state"]): string {
	switch (state) {
		case "fixed":
			return "bg-success-soft text-success-ink";
		case "reopened":
			return "bg-warning-soft text-warning-ink";
		case "expected":
			return "bg-muted text-muted-foreground";
		default:
			return "bg-primary/10 text-primary";
	}
}

export function FindingStateBadge({ state }: { state: Finding["state"] }) {
	const { t } = useTranslation("w443");
	return (
		<span className={cn("rounded-full px-2 py-0.5 text-xs font-medium", stateClass(state))} data-testid={`finding-state-${state}`}>
			{t(`states.${state}`)}
		</span>
	);
}

/** The URL of one finding on the Tracked tab. */
export function findingHref(id: string): string {
	return `?tab=findings&finding=${encodeURIComponent(id)}`;
}

/** Findings whose title, id, task or hypothesis holds the search text (M7-13). */
export function filterFindings(findings: readonly Finding[], q: string): Finding[] {
	const needle = q.trim().toLowerCase();
	if (!needle) return [...findings];
	return findings.filter((f) =>
		[f.title, f.id, f.hypothesis ?? "", ...f.tasks.map((x) => `${x.suite}/${x.task}`)].some((s) => s.toLowerCase().includes(needle)),
	);
}

/** The findings table: title, state, source, tasks count, last change. */
export function FindingList({
	findings,
	selected,
	onSelect,
}: {
	findings: Finding[];
	selected: string | null;
	onSelect: (id: string) => void;
}) {
	const { t } = useTranslation("w443");
	return (
		<table className="w-full text-sm" data-testid="finding-list">
			<thead className="text-muted-foreground text-left text-xs">
				<tr>
					<th className="py-2 pr-3 font-medium">{t("list.title")}</th>
					<th className="py-2 pr-3 font-medium">{t("list.state")}</th>
					<th className="py-2 pr-3 font-medium">{t("list.source")}</th>
					<th className="py-2 pr-3 font-medium">{t("list.tasks")}</th>
					<th className="py-2 pr-3 font-medium">{t("list.changed")}</th>
					<th className="py-2 font-medium">
						<span className="sr-only">{t("list.actions")}</span>
					</th>
				</tr>
			</thead>
			<tbody>
				{findings.map((f) => (
					<tr
						key={f.id}
						className={cn("border-border/60 border-t", selected === f.id && "bg-accent/50", isArchived(f) && ARCHIVED_ROW_CLASS)}
						data-testid={`finding-row-${f.id}`}
					>
						<td className="py-2 pr-3">
							<span className="flex flex-wrap items-center gap-x-2 gap-y-1">
								{/* A real link, so a finding can be opened in a new tab or shared (M7-32). */}
								<a
									href={findingHref(f.id)}
									className="text-primary text-left hover:underline"
									onClick={(e) => {
										if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
										e.preventDefault();
										onSelect(f.id);
									}}
									data-testid={`finding-link-${f.id}`}
								>
									{f.title}
								</a>
								<FindingArchivedPill finding={f} />
							</span>
						</td>
						<td className="py-2 pr-3">
							<FindingStateBadge state={f.state} />
						</td>
						<td className="py-2 pr-3">{t(`sources.${f.source}`)}</td>
						<td className="py-2 pr-3 tabular-nums">{f.tasks.length}</td>
						<td className="text-muted-foreground py-2 pr-3">{new Date(f.updated_at).toLocaleDateString()}</td>
						<td className="py-2 text-right">
							<FindingArchiveMenu finding={f} />
						</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}