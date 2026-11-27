import { useEffect, type RefObject } from "react";

/**
 * True when a keydown should close the dialog: Escape that no inner dialog has handled. The confirm
 * dialog (components/platform/confirmDialog.tsx) handles Escape in the capture phase with
 * preventDefault and stopPropagation, so a dialog that opened it stays open.
 */
export function closesOnKey(e: { key: string; defaultPrevented: boolean }): boolean {
	return e.key === "Escape" && !e.defaultPrevented;
}

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * The element Tab should move to so focus stays inside the dialog, or null to let the browser move
 * it. Pure over the list of focusable elements (M8-73, M1-37).
 */
export function trapTarget<T>(items: readonly T[], active: T | null, shift: boolean): T | null {
	if (items.length === 0) return null;
	const first = items[0];
	const last = items[items.length - 1];
	const inside = active !== null && items.includes(active);
	if (shift && (active === first || !inside)) return last;
	if (!shift && (active === last || !inside)) return first;
	return null;
}

/**
 * For a hand-made dialog or drawer while it is open: moves focus into panel (give the panel
 * tabIndex={-1}), keeps Tab and Shift+Tab inside it, closes on Escape (bubble phase, see
 * closesOnKey), and gives focus back to the element that had it when the dialog closes.
 */
export function useDialogFocus(panel: RefObject<HTMLElement | null>, onClose: () => void, open = true): void {
	useEffect(() => {
		if (!open || typeof document === "undefined") return;
		const prev = document.activeElement as HTMLElement | null;
		panel.current?.focus();
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Tab" && panel.current) {
				const items = Array.from(panel.current.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => el.offsetParent !== null);
				const next = trapTarget(items, document.activeElement as HTMLElement | null, e.shiftKey);
				if (next) {
					e.preventDefault();
					next.focus();
				}
				return;
			}
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
