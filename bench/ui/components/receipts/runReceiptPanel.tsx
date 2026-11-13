import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useGetRunReceiptsQuery } from "@/lib/store/apis/benchApi";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ReceiptEntries } from "./receiptEntries";

export interface RunReceiptPanelProps {
	runId: string;
	/** The probe condition shown; only that probe's receipt is listed. */
	probeId: string;
	onClose: () => void;
}

/** Drill-down for one run: GET /runs/{id}/receipts, filtered to the selected probe. */
export function RunReceiptPanel({ runId, probeId, onClose }: RunReceiptPanelProps) {
	const { t } = useTranslation("bench");
	const receipts = useGetRunReceiptsQuery(runId);
	const matching = receipts.data?.receipts.filter((r) => r.probe_id === probeId) ?? [];

	return (
		<div className="bg-card flex flex-col gap-3 rounded-sm border p-4" data-testid={`run-receipt-panel-${runId}`}>
			<div className="flex items-center justify-between gap-2">
				<h3 className="font-mono text-sm font-semibold">{t("receipts.entry.title", { run: runId })}</h3>
				<Button variant="ghost" size="sm" onClick={onClose} dataTestId="run-receipt-close" aria-label={t("receipts.entry.close")}>
					<X aria-hidden="true" />
				</Button>
			</div>
			{receipts.isLoading ? (
				<BlockSkeleton rows={8} testId="run-receipt-loading" />
			) : receipts.error ? (
				<LoadError
					title={t("receipts.entry.loadError", { run: runId })}
					error={receipts.error}
					onRetry={() => void receipts.refetch()}
					testId="run-receipt-error"
				/>
			) : matching.length === 0 ? (
				<EmptyNote testId="run-receipt-none">{t("receipts.entry.none", { probe: probeId })}</EmptyNote>
			) : (
				matching.map((receipt) => <ReceiptEntries key={`${receipt.probe_id}-${receipt.layer}`} receipt={receipt} />)
			)}
		</div>
	);
}