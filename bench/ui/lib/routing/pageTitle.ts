import { useEffect } from "react";

/**
 * Browser tab titles a page sets for itself (a guide's name, a shared item's label), keyed by
 * pathname. The shell's title effect runs after the page's (parent effects run after child
 * effects), so it reads this map first through pageTitle() and keeps the page's title.
 */
const overrides = new Map<string, string>();

function normal(pathname: string): string {
	return pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
}

/** Sets (or, with undefined, clears) the title override of a path. */
export function setPageTitleOverride(pathname: string, title: string | undefined): void {
	const key = normal(pathname);
	if (title) overrides.set(key, title);
	else overrides.delete(key);
}

/** The title a page set for a path, if any. */
export function pageTitleOverride(pathname: string): string | undefined {
	return overrides.get(normal(pathname));
}

/**
 * Gives the current page its own browser tab title while it is mounted; undefined (still
 * loading) leaves the shell's title. The override is cleared on unmount. The path is read from
 * window.location, so pages rendered outside a router (tests, the public frame) can use it too.
 */
export function usePageTitle(title: string | undefined): void {
	useEffect(() => {
		if (!title || typeof window === "undefined") return;
		const pathname = window.location.pathname;
		setPageTitleOverride(pathname, title);
		document.title = title;
		return () => setPageTitleOverride(pathname, undefined);
	}, [title]);
}