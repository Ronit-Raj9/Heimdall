import { closesOnKey } from "@/lib/hooks/useDialogFocus";
import { useFocusTrap } from "@/lib/hooks/useFocusTrap";
import { cn } from "@/lib/utils";
import { X } from "lucide-react";
import { useEffect, useId, useRef, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";

export interface DialogProps {
	open: boolean;
	onOpenChange(open: boolean): void;
	/** Becomes the dialog's name (aria-labelledby). */
	title: ReactNode;
	/** Becomes the dialog's description (aria-describedby). */
	description?: ReactNode;
	children?: ReactNode;
	/** Buttons, right aligned; sticks to the bottom on phones. */
	footer?: ReactNode;
	/** sm max-w-md, md max-w-lg (default), lg max-w-2xl. */
	size?: "sm" | "md" | "lg";
	/** right: a drawer on the right edge, full height. */
	side?: "center" | "right";
	/** Name of the close X; pass "Close", never "Cancel". */
	closeLabel: string;
	/** alertdialog: for confirms; the overlay click does not close it. */
	role?: "dialog" | "alertdialog";
	/** Element to focus when it opens; default the panel itself. */
	initialFocus?: RefObject<HTMLElement | null>;
	/** Hide the close X (confirms have their own Cancel). */
	hideClose?: boolean;
	className?: string;
	testId: string;
}

const SIZE = { sm: "max-w-md", md: "max-w-lg", lg: "max-w-2xl" } as const;

// How many dialogs are open: the app root stays inert and the page stays locked until the last closes.
let openCount = 0;
let savedOverflow = "";

/** Makes everything outside the dialogs inert and stops the page from scrolling. Returns the undo. */
export function lockBackground(doc: Document = document): () => void {
	if (openCount === 0) {
		savedOverflow = doc.body.style.overflow;
		doc.body.style.overflow = "hidden";
		doc.getElementById("root")?.setAttribute("inert", "");
	}
	openCount += 1;
	let done = false;
	return () => {
		if (done) return;
		done = true;
		openCount -= 1;
		if (openCount === 0) {
			doc.body.style.overflow = savedOverflow;
			doc.getElementById("root")?.removeAttribute("inert");
		}
	};
}

/**
 * The one modal of the app, built by hand (no dialog package): a portal into document.body,
 * focus moved in and kept in (Tab wraps), Escape and the overlay close it, focus returns to the
 * opener, the app behind is inert and does not scroll. A confirm opened from inside handles
 * Escape in the capture phase first, so only the confirm closes.
 */
export function Dialog({
	open,
	onOpenChange,
	title,
	description,
	children,
	footer,
	size = "md",
	side = "center",
	closeLabel,
	role = "dialog",
	initialFocus,
	hideClose,
	className,
	testId,
}: DialogProps) {
	const panel = useRef<HTMLDivElement>(null);
	const titleId = useId();
	const descId = useId();
	const change = useRef(onOpenChange);
	change.current = onOpenChange;

	useEffect(() => {
		if (!open || typeof document === "undefined") return;
		const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
		const unlock = lockBackground();
		(initialFocus?.current ?? panel.current)?.focus();
		const onKey = (e: KeyboardEvent) => {
			if (!closesOnKey(e)) return;
			// Only the top-most dialog closes.
			const top = document.querySelectorAll('[data-heimdall-dialog="open"]');
			if (top[top.length - 1] !== panel.current) return;
			e.preventDefault();
			change.current(false);
		};
		window.addEventListener("keydown", onKey);
		return () => {
			window.removeEventListener("keydown", onKey);
			unlock();
			if (opener && document.contains(opener)) opener.focus();
		};
		// initialFocus is a ref: read once per opening.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [open]);

	useFocusTrap(panel, open);

	if (!open) return null;
	const right = side === "right";
	const tree = (
		<div
			className={cn("fixed inset-0 z-[100] flex", right ? "justify-end" : "items-start justify-center overflow-y-auto p-4 sm:items-center")}
			data-testid={`${testId}-backdrop`}
		>
			<div
				className="fixed inset-0 bg-[var(--overlay,rgb(0_0_0/0.4))] backdrop-blur-[2px] motion-safe:animate-in motion-safe:fade-in motion-safe:duration-150"
				aria-hidden="true"
				data-testid={`${testId}-overlay`}
				onMouseDown={() => role !== "alertdialog" && change.current(false)}
			/>
			<div
				ref={panel}
				role={role}
				aria-modal="true"
				aria-labelledby={titleId}
				aria-describedby={description ? descId : undefined}
				tabIndex={-1}
				data-heimdall-dialog="open"
				data-testid={testId}
				className={cn(
					"bg-card text-card-foreground relative z-10 flex w-full flex-col border shadow-xl outline-none",
					right ? "h-full max-w-md overflow-y-auto sm:max-w-lg" : cn("rounded-xl", SIZE[size]),
					className,
				)}
			>
				<div className="flex items-start justify-between gap-4 px-5 pt-5 pb-3">
					<div className="flex min-w-0 flex-col gap-1">
						<h2 id={titleId} className="text-base font-semibold tracking-tight break-words">
							{title}
						</h2>
						{description && (
							<div id={descId} className="text-muted-foreground text-sm break-words">
								{description}
							</div>
						)}
					</div>
					{!hideClose && (
						<button
							type="button"
							onClick={() => change.current(false)}
							className="text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-ring rounded-md p-1 focus-visible:ring-2"
							aria-label={closeLabel}
							data-testid={`${testId}-close`}
						>
							<X className="size-4" aria-hidden="true" />
						</button>
					)}
				</div>
				{children !== undefined && <div className={cn("min-w-0 px-5", footer ? "pb-3" : "pb-5", right && "flex-1")}>{children}</div>}
				{footer && (
					<div className="bg-card sticky bottom-0 flex flex-wrap items-center justify-end gap-2 rounded-b-xl border-t px-5 py-3 sm:static sm:border-t-0 sm:pt-2 sm:pb-5">
						{footer}
					</div>
				)}
			</div>
		</div>
	);
	// Without a document (server render in tests) the dialog renders in place.
	return typeof document === "undefined" ? tree : createPortal(tree, document.body);
}
