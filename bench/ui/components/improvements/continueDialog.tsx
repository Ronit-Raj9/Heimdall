import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useContinueEstimateMutation, useContinueImprovementMutation } from "@/lib/store/apis/improveApi";
import type { ContinueOffer, ContinueRequest } from "@/lib/types/improve";
import { Dialog } from "@/components/ui/dialog";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { money } from "./improve.utils";
import "./w45impI18n";

/** The second look the dialog asks the server to price: two more tries on each task and new tasks (A2.11). */
export const CONTINUE_REQUEST: ContinueRequest = { extra_trials: 2, new_tasks: true };

export interface ContinueDialogBodyProps {
	offer?: ContinueOffer;
	loading?: boolean;
	error?: string;
	busy?: boolean;
	onConfirm(): void;
	onCancel(): void;
}

/** The dialog's content without data fetching: the offer text and cost, Continue and Cancel. Exported for tests. */
export function ContinueDialogBody({ offer, loading, error, busy, onConfirm, onCancel }: ContinueDialogBodyProps) {
	const { t } = useTranslation("w45imp");
	// The shared Dialog (Q-31): focus trapped inside, Escape closes, focus returns to the opener.
	return (
		<Dialog
			open
			onOpenChange={(open) => !open && onCancel()}
			size="sm"
			closeLabel={t("continue.cancel")}
			hideClose
			title={t("continue.title")}
			testId="continue-dialog"
			footer={
				<>
					<Button type="button" variant="ghost" onClick={onCancel} data-testid="continue-cancel">
						{t("continue.cancel")}
					</Button>
					<Button type="button" onClick={onConfirm} disabled={!offer || loading || busy} data-testid="continue-confirm">
						{t("continue.submit")}
					</Button>
				</>
			}
		>
			<div className="flex flex-col gap-3 text-left">
				{loading && <p className="text-muted-foreground text-sm">{t("continue.loading")}</p>}
				{offer?.text && <p className="text-foreground text-sm">{offer.text}</p>}
				{offer && (
					<p className="text-foreground text-sm" data-testid="continue-text">
						{t("continue.confirm", { usd: money(offer.usd?.[1]), runs: offer.runs ?? 0 })}
					</p>
				)}
				{error && (
					<p className="text-chart-error-ink text-sm" role="alert" data-testid="continue-error">
						{error}
					</p>
				)}
			</div>
		</Dialog>
	);
}

/**
 * Continue with more budget (org admins): prices the second look when it opens (nothing is spent),
 * and sends the request only on Continue, raising the spending limit by the offer's high cost.
 */
export function ContinueDialog({ id, onClose }: { id: string; onClose(): void }) {
	const [estimate, est] = useContinueEstimateMutation();
	const [send, sent] = useContinueImprovementMutation();
	useEffect(() => {
		void estimate({ id, ...CONTINUE_REQUEST });
	}, [estimate, id]);
	const offer = est.data;
	const confirm = async () => {
		if (!offer) return;
		try {
			await send({ id, ...CONTINUE_REQUEST, extra_budget_usd: offer.usd?.[1] ?? 0 }).unwrap();
			onClose();
		} catch {
			// sent.error renders the refusal.
		}
	};
	const err = est.error ?? sent.error;
	return (
		<ContinueDialogBody
				offer={offer}
				loading={est.isLoading}
				error={err ? getBenchErrorMessage(err) : undefined}
				busy={sent.isLoading}
				onConfirm={() => void confirm()}
				onCancel={onClose}
			/>
	);
}