import { ChevronDown } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import "./reportCardI18n";

/** "codex v2, Nov 21 (40 runs)": what the report card shows, in one line (M2-34). Parts left out when unknown. */
export function picksSummary(p: { name?: string | null; date?: string | null; runs?: number | null }): string {
	const head = [p.name, p.date].filter(Boolean).join(", ");
	const runs = typeof p.runs === "number" && p.runs > 0 ? `${p.runs.toLocaleString("en-US")} ${p.runs === 1 ? "run" : "runs"}` : "";
	if (!head) return runs;
	return runs ? `${head} (${runs})` : head;
}

/**
 * The report card picks as one line (M2-34): "Showing: <what>. Change" and, behind Change, the caller's own selects
 * (version, model, evaluation). The selects stay the caller's; this only folds them, so the score comes first.
 */
export function ReportPicks({ summary, children, testId = "report-picks" }: { summary: string; children: ReactNode; testId?: string }) {
	const { t } = useTranslation("reportCard");
	const [open, setOpen] = useState(false);
	const panelId = useId();
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid={testId}>
			<p className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-sm">
				<span className="text-muted-foreground">{t("picks.showing")}</span>
				<span className="min-w-0 font-medium break-words" data-testid={`${testId}-summary`}>
					{summary}
				</span>
				<button
					type="button"
					className="text-primary inline-flex min-h-9 items-center gap-1 font-medium hover:underline focus-visible:ring-2 focus-visible:ring-(--focus) focus-visible:outline-none"
					aria-expanded={open}
					aria-controls={panelId}
					onClick={() => setOpen((o) => !o)}
					data-testid={`${testId}-change`}
				>
					{t("picks.change")}
					<ChevronDown className={open ? "size-3.5 rotate-180" : "size-3.5"} aria-hidden="true" />
				</button>
			</p>
			<div id={panelId} hidden={!open} className="bg-card flex flex-wrap items-end gap-3 rounded-lg border p-3">
				{children}
			</div>
		</div>
	);
}
