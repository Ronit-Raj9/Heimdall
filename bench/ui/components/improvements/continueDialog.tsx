import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useContinueEstimateMutation, useContinueImprovementMutation } from "@/lib/store/apis/improveApi";
import type { ContinueOffer, ContinueRequest } from "@/lib/types/improve";
import { useEffect, useId } from "react";
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
	const ids = useId();
	return (
		<div
			role="dialog"
			aria-modal="true"
			aria-labelledby={`${ids}-title`}
			className="bg-card text-card-foreground flex w-full max-w-md flex-col gap-3 rounded-lg border p-5 text-left shadow-lg"
			data-testid="continue-dialog"
		>
			<h2 id={`${ids}-title`} className="text-lg font-semibold">
				{t("continue.title")}
			</h2>
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
			<div className="flex justify-end gap-2">
				<Button type="button" variant="ghost" onClick={onCancel} data-testid="continue-cancel">
					{t("continue.cancel")}
				</Button>
				<Button type="button" onClick={onConfirm} disabled={!offer || loading || busy} data-testid="continue-confirm">
					{t("continue.submit")}
				</Button>
			</div>
		</div>
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
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose]);
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
		<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" data-testid="continue-backdrop">
			<ContinueDialogBody
				offer={offer}
				loading={est.isLoading}
				error={err ? getBenchErrorMessage(err) : undefined}
				busy={sent.isLoading}
				onConfirm={() => void confirm()}
				onCancel={onClose}
			/>
		</div>
	);
}