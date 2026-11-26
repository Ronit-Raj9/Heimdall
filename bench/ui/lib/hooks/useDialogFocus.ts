import { useEffect, type RefObject } from "react";

/**
 * True when a keydown should close the dialog: Escape that no inner dialog has handled. The confirm
 * dialog (components/platform/confirmDialog.tsx) handles Escape in the capture phase with
 * preventDefault and stopPropagation, so a dialog that opened it stays open.
 */
export function closesOnKey(e: { key: string; defaultPrevented: boolean }): boolean {
	return e.key === "Escape" && !e.defaultPrevented;
}

/**
 * For a hand-made dialog or drawer while it is open: moves focus into panel (give the panel
 * tabIndex={-1}), closes on Escape (bubble phase, see closesOnKey), and gives focus back to the
 * element that had it when the dialog closes.
 */
export function useDialogFocus(panel: RefObject<HTMLElement | null>, onClose: () => void, open = true): void {
	useEffect(() => {
		if (!open || typeof document === "undefined") return;
		const prev = document.activeElement as HTMLElement | null;
		panel.current?.focus();
		const onKey = (e: KeyboardEvent) => {
			if (!closesOnKey(e)) return;
			e.preventDefault();
			onClose();
		};
		window.addEventListener("keydown", onKey);
		return () => {
			window.removeEventListener("keydown", onKey);
			if (prev && document.contains(prev)) prev.focus();
		};
	}, [panel, onClose, open]);
}
