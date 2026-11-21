import { cn } from "@/lib/utils";
import { X } from "lucide-react";
import { useEffect, type ReactNode } from "react";
import { createPortal } from "react-dom";

export interface GovDialogProps {
	open: boolean;
	onClose(): void;
	title: ReactNode;
	description?: ReactNode;
	closeLabel: string;
	children: ReactNode;
	className?: string;
	testId?: string;
}

/** A small centred modal: overlay click and Escape close it. Local until a shared dialog primitive lands. */
export function GovDialog({ open, onClose, title, description, closeLabel, children, className, testId }: GovDialogProps) {
	useEffect(() => {
		if (!open) return;
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [open, onClose]);
	if (!open || typeof document === "undefined") return null;
	return createPortal(
		<div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 sm:items-center" data-testid={testId}>
			<div className="fixed inset-0 bg-black/40 backdrop-blur-[2px]" onClick={onClose} aria-hidden="true" />
			<div
				role="dialog"
				aria-modal="true"
				className={cn("bg-background relative z-10 w-full max-w-lg rounded-xl border p-6 shadow-xl", className)}
			>
				<div className="mb-4 flex items-start justify-between gap-4">
					<div className="flex flex-col gap-1">
						<h2 className="text-lg font-semibold tracking-tight">{title}</h2>
						{description && <p className="text-muted-foreground text-sm">{description}</p>}
					</div>
					<button
						type="button"
						onClick={onClose}
						className="text-muted-foreground hover:bg-muted hover:text-foreground rounded-md p-1"
						aria-label={closeLabel}
					>
						<X className="size-4" aria-hidden="true" />
					</button>
				</div>
				{children}
			</div>
		</div>,
		document.body,
	);
}
