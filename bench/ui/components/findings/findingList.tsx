import "@/app/inbox/views/w443I18n";

import type { Finding } from "@/lib/store/apis/findingsApi";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

/** The badge colour of each finding state; reopened uses the warning colour. */
export function stateClass(state: Finding["state"]): string {
	switch (state) {
		case "fixed":
			return "bg-chart-success/15 text-chart-success-ink";
		case "reopened":
			return "bg-chart-warning/15 text-chart-warning-ink";
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
					<th className="py-2 font-medium">{t("list.changed")}</th>
				</tr>
			</thead>
			<tbody>
				{findings.map((f) => (
					<tr
						key={f.id}
						className={cn("border-border/60 border-t", selected === f.id && "bg-accent/50")}
						data-testid={`finding-row-${f.id}`}
					>
						<td className="py-2 pr-3">
							<button type="button" className="text-primary text-left hover:underline" onClick={() => onSelect(f.id)}>
								{f.title}
							</button>
						</td>
						<td className="py-2 pr-3">
							<FindingStateBadge state={f.state} />
						</td>
						<td className="py-2 pr-3">{t(`sources.${f.source}`)}</td>
						<td className="py-2 pr-3 tabular-nums">{f.tasks.length}</td>
						<td className="text-muted-foreground py-2">{new Date(f.updated_at).toLocaleDateString()}</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}