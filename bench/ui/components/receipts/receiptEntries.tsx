import { RECEIPT_STEPS, type Receipt, type StepState } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

const STATE_CLASS: Record<StepState, string> = {
	yes: "border-chart-success/50 bg-chart-success/10 text-chart-success-ink",
	no: "border-chart-error/50 bg-chart-error/10 text-chart-error-ink",
	unknown: "bench-hatched text-muted-foreground border-dashed",
	not_applicable: "text-muted-foreground border-transparent",
};

/** One run's receipt for one probe: each step's state, detail and the evidence ids behind it, as inert text. */
export function ReceiptEntries({ receipt }: { receipt: Receipt }) {
	const { t } = useTranslation("bench");
	return (
		<ol className="flex flex-col divide-y rounded-sm border" data-testid={`receipt-entries-${receipt.probe_id}`}>
			{RECEIPT_STEPS.map((step, index) => {
				const entry = receipt.steps[step];
				const state: StepState = entry?.state ?? "unknown";
				const na = state === "not_applicable";
				return (
					<li
						key={step}
						data-testid={`receipt-entry-${step}`}
						data-state={state}
						className={cn("grid gap-2 px-3 py-2 text-xs sm:grid-cols-[11rem_5rem_1fr]", na && "bg-muted/40 text-muted-foreground")}
					>
						<span className={cn("font-medium", na && "font-normal")}>
							<span className="text-muted-foreground mr-2 font-mono text-[11px]">{t("receipts.stepCode", { index })}</span>
							{t(`receipts.steps.${step}`)}
						</span>
						<span>
							<span className={cn("inline-flex rounded-sm border px-1.5 py-0.5 text-[11px] font-medium", STATE_CLASS[state])}>
								{t(`receipts.entry.state.${state}`)}
							</span>
						</span>
						<div className="flex min-w-0 flex-col gap-1">
							{entry?.detail && <span className="break-words whitespace-pre-wrap">{entry.detail}</span>}
							{!na && <EvidenceIds ids={entry?.evidence} />}
						</div>
					</li>
				);
			})}
		</ol>
	);
}

function EvidenceIds({ ids }: { ids: string[] | undefined }) {
	const { t } = useTranslation("bench");
	if (!ids || ids.length === 0) return <span className="text-muted-foreground italic">{t("receipts.entry.noEvidence")}</span>;
	return (
		<ul className="flex flex-wrap gap-1" aria-label={t("receipts.entry.evidence")}>
			{ids.map((id) => (
				<li
					key={id}
					className="bg-muted/60 max-w-full rounded-sm border px-1.5 py-0.5 font-mono text-[11px] break-all"
					data-testid="receipt-evidence-id"
				>
					{id}
				</li>
			))}
		</ul>
	);
}