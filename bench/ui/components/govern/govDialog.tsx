import { Dialog } from "@/components/ui/dialog";
import type { ReactNode } from "react";

export interface GovDialogProps {
	open: boolean;
	onClose(): void;
	title: ReactNode;
	description?: ReactNode;
	/** Name of the close X: "Close". */
	closeLabel: string;
	children: ReactNode;
	className?: string;
	testId?: string;
}

/**
 * A small centred modal, now a thin wrapper over the shared Dialog (focus trapped and returned,
 * Escape and the overlay close it). Kept so its callers need no change.
 */
export function GovDialog({ open, onClose, title, description, closeLabel, children, className, testId }: GovDialogProps) {
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => !next && onClose()}
			title={title}
			description={description}
			closeLabel={closeLabel}
			className={className}
			testId={testId ?? "gov-dialog"}
		>
			{children}
		</Dialog>
	);
}
