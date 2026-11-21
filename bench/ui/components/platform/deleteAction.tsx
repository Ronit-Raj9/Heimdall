import { useConfirm } from "@/components/platform/confirmDialog";
import { Button } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { Trash2 } from "lucide-react";
import { useState } from "react";

export interface DeleteActionProps {
	label: string;
	/** The confirm dialog: title, body and its two buttons. */
	confirmTitle: string;
	confirmBody: string;
	confirmLabel: string;
	cancelLabel: string;
	/** Why the delete is not possible right now (still running), shown as the button's tooltip and note. */
	busyReason?: string;
	/** Shown in place of the button for a role below admin. */
	adminOnly: string;
	/** Formats the server's refusal, for example "Could not delete: {{error}}". */
	errorText(message: string): string;
	/** Runs the delete; resolves when it is done, throws the RTK Query error otherwise. */
	onDelete(): Promise<unknown>;
	/** After a successful delete: navigate away. */
	onDeleted(): void;
	testId: string;
}

/**
 * A header Delete action for admins: asks in the confirm dialog, deletes, then hands over to
 * onDeleted. Disabled with the reason while the item still runs; the server's 409 message shows
 * when the state changed under the page.
 */
export function DeleteAction({
	label,
	confirmTitle,
	confirmBody,
	confirmLabel,
	cancelLabel,
	busyReason,
	adminOnly,
	errorText,
	onDelete,
	onDeleted,
	testId,
}: DeleteActionProps) {
	const session = useSession();
	const confirm = useConfirm();
	const [pending, setPending] = useState(false);
	const [error, setError] = useState<string | null>(null);
	if (!canUse(session, "admin")) {
		return (
			<span className="sr-only" data-testid={`${testId}-admin-only`}>
				{adminOnly}
			</span>
		);
	}
	const run = async () => {
		const ok = await confirm(confirmBody, { title: confirmTitle, confirmLabel, cancelLabel, destructive: true });
		if (!ok) return;
		setError(null);
		setPending(true);
		try {
			await onDelete();
			onDeleted();
		} catch (e) {
			setError(errorText(getBenchErrorMessage(e)));
		} finally {
			setPending(false);
		}
	};
	return (
		<span className="flex items-center gap-2">
			{error && (
				<span role="alert" className="text-chart-error-ink max-w-80 text-xs" data-testid={`${testId}-error`}>
					{error}
				</span>
			)}
			{busyReason && !error && (
				<span className="text-muted-foreground hidden max-w-64 text-xs lg:inline" data-testid={`${testId}-busy`}>
					{busyReason}
				</span>
			)}
			<Button
				size="sm"
				variant="ghost"
				className="text-chart-error-ink hover:text-chart-error-ink"
				disabled={Boolean(busyReason) || pending}
				isLoading={pending}
				title={busyReason}
				onClick={() => void run()}
				dataTestId={testId}
			>
				<Trash2 aria-hidden="true" />
				{label}
			</Button>
		</span>
	);
}
