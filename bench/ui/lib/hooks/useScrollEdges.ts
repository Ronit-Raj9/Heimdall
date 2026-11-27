import { useEffect, type RefObject } from "react";

/** Which sides of a sideways scroll box hide content. Pure, for tests. */
export function scrollEdges(el: { scrollLeft: number; scrollWidth: number; clientWidth: number }): { start: boolean; end: boolean } {
	const overflow = el.scrollWidth - el.clientWidth > 1;
	if (!overflow) return { start: false, end: false };
	return { start: el.scrollLeft > 1, end: el.scrollLeft + el.clientWidth < el.scrollWidth - 1 };
}

/**
 * Marks a sideways scroll box with data-overflow-start and data-overflow-end while content hides
 * past that edge; app/globals.css fades those edges (X-95). Updates on scroll and on resize of the
 * box or its content. Pass start: false where pinned leading columns must never fade.
 */
export function useScrollEdges(ref: RefObject<HTMLElement | null>, opts: { start?: boolean } = {}): void {
	const fadeStart = opts.start ?? true;
	useEffect(() => {
		const el = ref.current;
		if (!el) return;
		const update = () => {
			const edges = scrollEdges(el);
			el.dataset.overflowStart = String(fadeStart && edges.start);
			el.dataset.overflowEnd = String(edges.end);
		};
		update();
		el.addEventListener("scroll", update, { passive: true });
		const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(update);
		observer?.observe(el);
		if (el.firstElementChild) observer?.observe(el.firstElementChild);
		return () => {
			el.removeEventListener("scroll", update);
			observer?.disconnect();
		};
	}, [ref, fadeStart]);
}
