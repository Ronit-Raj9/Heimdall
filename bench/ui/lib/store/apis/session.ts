// What the request layer needs to know about the session: whether the server runs hosted (session
// cookie plus CSRF header) or local (write token), and the CSRF value. Set from GET /auth/me by the
// session provider; read by prepareBenchHeaders. Pure module state so tests can set it directly.
import type { AuthMode, Role } from "@/lib/types/bench";

/** Header carrying the session's CSRF value on every cookie-authenticated write. */
export const CSRF_HEADER = "X-Heimdall-CSRF";

interface AuthState {
	mode: AuthMode;
	csrf: string;
}

let authState: AuthState = { mode: "local", csrf: "" };

/** The auth mode and CSRF value the request layer uses. Local until /auth/me says otherwise. */
export function getAuthState(): Readonly<AuthState> {
	return authState;
}

/** Records the mode and CSRF value from /auth/me. */
export function setAuthState(next: { mode: AuthMode; csrf?: string }): void {
	authState = { mode: next.mode, csrf: next.csrf ?? "" };
}

type UnauthorizedListener = () => void;
let unauthorizedListener: UnauthorizedListener | null = null;

/** The session provider registers what a hosted 401 does (refresh /auth/me, then the login page). */
export function onUnauthorized(listener: UnauthorizedListener | null): void {
	unauthorizedListener = listener;
}

/** Called by the base query when a hosted request answers 401. Auth routes never trigger it. */
export function notifyUnauthorized(url: string): void {
	if (authState.mode !== "hosted") return;
	if (isAuthRoute(url)) return;
	unauthorizedListener?.();
}

/** True for /auth/* routes: their 401 is the answer (bad password, signed out), not an expired session. */
export function isAuthRoute(url: string): boolean {
	const path = url.split("?")[0] ?? "";
	return /(^|\/)auth\//.test(path.replace(/^\/api\/bench/, ""));
}

const ROLE_RANK: Record<Role, number> = { viewer: 1, member: 2, admin: 3, owner: 4 };

/** True when role grants everything min grants; false for a missing or unknown role. */
export function roleAtLeast(role: Role | undefined | null, min: Role): boolean {
	if (!role || !(role in ROLE_RANK)) return false;
	return ROLE_RANK[role] >= ROLE_RANK[min];
}

/** Every role, least privileged first: the order of role pickers. */
export const ROLES: readonly Role[] = ["viewer", "member", "admin", "owner"];

/** The HTTP status of a failed RTK Query request, when it has one. */
export function errorStatus(error: unknown): number | undefined {
	if (error && typeof error === "object" && "status" in error) {
		const status = (error as { status: unknown }).status;
		if (typeof status === "number") return status;
		// fetchBaseQuery reports an unparsable body as PARSING_ERROR with the real status beside it.
		const original = (error as { originalStatus?: unknown }).originalStatus;
		if (typeof original === "number") return original;
	}
	return undefined;
}

/** The server refused the caller's role (403). */
export function isForbidden(error: unknown): boolean {
	return errorStatus(error) === 403;
}

/** The request had no valid session or token (401). */
export function isUnauthorized(error: unknown): boolean {
	return errorStatus(error) === 401;
}

/** The route does not exist on this server (404): an older server, or a feature switched off. */
export function isMissingRoute(error: unknown): boolean {
	return errorStatus(error) === 404;
}

/**
 * A safe place to go after signing in: a path on this site ("/x", not "//host" or "https://..."),
 * never the login or sign-up page itself (an invitation page is fine). Anything else is "/".
 */
export function safeReturnTo(raw: unknown): string {
	if (typeof raw !== "string" || raw.length === 0 || raw.length > 2048) return "/";
	if (!raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return "/";
	if (/[\u0000-\u001f]/.test(raw)) return "/";
	const path = raw.split(/[?#]/)[0] ?? "";
	if (path === "/login" || path === "/signup") return "/";
	return raw;
}

/** Paths that render without the app shell and never redirect to the login page. */
export function isAuthPage(pathname: string): boolean {
	return pathname === "/login" || pathname === "/signup" || pathname.startsWith("/invite/");
}