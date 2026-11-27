import { useEffect, type RefObject } from "react";

/** What can take focus inside a dialog or drawer. Hidden elements are filtered out at use. */
export const FOCUSABLE =
	'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])';

/**
 * Where Tab should go inside a trap of `count` focusable elements when focus is at `index`
 * (-1 when focus is on the panel itself or outside the list). Returns the index to focus, or
 * null when the browser's own move is fine (it stays inside).
 */
export function nextFocusIndex(count: number, index: number, shift: boolean): number | null {
	if (count <= 0) return null;
	if (index < 0) return shift ? count - 1 : 0;
	if (shift && index === 0) return count - 1;
	if (!shift && index === count - 1) return 0;
	return null;
}

/** The focusable, visible elements of a panel, in tab order. */
export function focusableIn(panel: HTMLElement): HTMLElement[] {
	return Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
		(el) => el.offsetParent !== null || el === document.activeElement,
	);
}

/**
 * Keeps Tab and Shift+Tab inside `panel` while `active`: Tab on the last element goes to the
 * first, Shift+Tab on the first goes to the last. Listens on the document in the capture phase,
 * so it also catches Tab when focus sits on the panel itself.
 */
export function useFocusTrap(panel: RefObject<HTMLElement | null>, active = true): void {
	useEffect(() => {
		if (!active || typeof document === "undefined") return;
		const onKey = (e: KeyboardEvent) => {
			const el = panel.current;
			if (e.key !== "Tab" || !el) return;
			// Only the top-most trap acts: a dialog opened from a dialog owns the keyboard.
			if (!el.contains(document.activeElement) && document.activeElement !== document.body && document.activeElement !== el) {
				const top = document.activeElement?.closest('[aria-modal="true"]');
				if (top && top !== el) return;
			}
			const items = focusableIn(el);
			const next = nextFocusIndex(items.length, items.indexOf(document.activeElement as HTMLElement), e.shiftKey);
			if (items.length === 0) {
				e.preventDefault();
				el.focus();
				return;
			}
			if (next !== null) {
				e.preventDefault();
				items[next].focus();
			}
		};
		document.addEventListener("keydown", onKey, true);
		return () => document.removeEventListener("keydown", onKey, true);
	}, [panel, active]);
}
