// The public site (lane Y08): pages a visitor reads without signing in. They call only the
// /public/* API (no organization data) and render in the public frame (components/site).

/** Pages that are public for everyone, signed in or not, in the public frame. */
export const PUBLIC_PAGES = ["/library", "/pricing", "/downloads", "/security", "/terms", "/privacy"] as const;

export type PublicPage = (typeof PUBLIC_PAGES)[number];

/** Whether pathname is one of PUBLIC_PAGES (a trailing slash is the same page). */
export function isPublicPage(pathname: string): boolean {
	const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
	return (PUBLIC_PAGES as readonly string[]).includes(path);
}

/**
 * Pages a hosted visitor who is not signed in reads in the public frame instead of being sent to
 * the login page; a signed-in user gets the app's own page there ("/" is Home, /docs the in-app
 * guides).
 */
export function isVisitorPage(pathname: string): boolean {
	const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
	return path === "/" || path === "/docs";
}
