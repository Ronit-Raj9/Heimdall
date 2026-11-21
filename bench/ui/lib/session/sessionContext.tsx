// The session every screen reads: local or hosted, who is signed in, in which organization, with
// which role. The default is local mode, so screens rendered without the provider (tests, the
// standalone bench before the platform) behave exactly as before.
import { errorStatus, getAuthState, isAuthPage, isUnauthorized, roleAtLeast } from "@/lib/store/apis/session";
import type { AuthConfig, AuthMe, AuthMode, OrgSummary, PlatformUser, PrincipalKind, Role } from "@/lib/types/bench";
import { createContext, useContext } from "react";

export interface SessionInfo {
	/**
	 * loading until /auth/me answers; local mode never waits. error: /auth/me failed with neither a
	 * 401 nor a 404 before any answer, so the mode is unknown and the app offers a retry.
	 */
	status: "loading" | "ready" | "error";
	authMode: AuthMode;
	kind: PrincipalKind;
	user?: PlatformUser;
	/** The organization requests act in. */
	org?: OrgSummary;
	/** Organizations the user belongs to (the org switcher). */
	orgs: OrgSummary[];
	/** The caller's role in org. Local mode is the owner of its one organization. */
	role?: Role;
	/** Hosted, with a session or token. */
	signedIn: boolean;
	/** Hosted, anonymous visitor of a public-read organization: read only, no write controls. */
	demo: boolean;
	signup: AuthConfig["signup"];
	github: boolean;
	/** An empty install: the first sign-up creates the owner, whatever the signup policy. */
	bootstrap: boolean;
}

export const LOCAL_SESSION: SessionInfo = {
	status: "ready",
	authMode: "local",
	kind: "local",
	orgs: [],
	role: "owner",
	signedIn: false,
	demo: false,
	signup: undefined,
	github: false,
	bootstrap: false,
};

export interface SessionSources {
	/** GET /auth/me. */
	me?: AuthMe;
	meError?: unknown;
	meLoading: boolean;
	/** GET /auth/config: sign-up policy and GitHub, for visitors who are not signed in. */
	config?: AuthConfig;
	/** An anonymous read of an org-scoped route answered: this server serves a public demo. */
	demoReadable?: boolean;
}

/**
 * The session from GET /auth/me. A 404 (a server from before the platform) or auth_mode local is
 * local mode; a 401 is a hosted server with nobody signed in, which is a public demo when an
 * anonymous read succeeds.
 */
export function sessionFromMe({ me, meError, meLoading, config, demoReadable = false }: SessionSources): SessionInfo {
	const offers = { signup: config?.signup, github: Boolean(config?.github), bootstrap: Boolean(config?.bootstrap) };
	// A 401 wins over earlier data: the session ended since the last answer.
	if (isUnauthorized(meError)) {
		// Still loading while the sign-in options and the demo read are on their way.
		const status = meLoading ? "loading" : "ready";
		return { ...LOCAL_SESSION, ...offers, status, authMode: "hosted", kind: "anonymous", role: undefined, demo: demoReadable };
	}
	if (me) {
		if (me.auth_mode !== "hosted") return { ...LOCAL_SESSION };
		const kind = me.kind ?? "anonymous";
		const signedIn = kind === "user" || kind === "token";
		const orgs: OrgSummary[] = (me.orgs ?? []).map((org) => ({ ...org }));
		// Only the server's current org is the request scope. A membership the session has not
		// selected (after leaving the current org) is offered by the switcher, never shown as if
		// requests already acted in it: they resolve no org until one is chosen.
		const org = orgs.find((candidate) => candidate.id === me.current_org) ?? (kind === "token" && orgs.length === 1 ? orgs[0] : undefined);
		return {
			status: "ready",
			authMode: "hosted",
			kind,
			user: me.user,
			org,
			orgs,
			role: signedIn ? (me.role ?? org?.role) : undefined,
			signedIn,
			demo: false,
			...offers,
		};
	}
	if (meLoading) {
		// Until /auth/me answers, keep whatever the request layer already knows.
		return getAuthState().mode === "hosted"
			? { ...LOCAL_SESSION, status: "loading", authMode: "hosted", kind: "anonymous", role: undefined }
			: { ...LOCAL_SESSION, status: "loading" };
	}
	// 404 (no platform routes): local mode, today's behaviour. Any other failure (a transport error,
	// a 5xx) proves nothing about the mode: a hosted server would still want its session and CSRF
	// headers, so no local controls are offered, only a retry.
	if (meError === undefined || errorStatus(meError) === 404) return { ...LOCAL_SESSION };
	return { ...LOCAL_SESSION, status: "error", authMode: "hosted", kind: "anonymous", role: undefined };
}

/**
 * Whether the session may use a control that needs at least min. Local mode always may (the write
 * token is the gate); a demo visitor or anonymous caller never may.
 */
export function canUse(session: SessionInfo, min: Role = "member"): boolean {
	if (session.authMode === "local") return true;
	if (!session.signedIn) return false;
	return roleAtLeast(session.role, min);
}

/** Whether a visitor without an invitation may use the signup form: open signup or an empty install. */
export function signupOffered(session: SessionInfo): boolean {
	return session.signup === "open" || session.bootstrap;
}

/** True when this tab should show the write-token box: local mode only. */
export function usesWriteToken(session: SessionInfo): boolean {
	return session.authMode === "local";
}

export const SessionContext = createContext<SessionInfo>(LOCAL_SESSION);

/** The current session. Local mode when no provider is mounted. */
export function useSession(): SessionInfo {
	return useContext(SessionContext);
}

/**
 * What the app frame shows for a path: the auth pages without the shell, the login page for a
 * hosted visitor who is neither signed in nor reading a public demo, else the app shell.
 */
export function frameFor(session: SessionInfo, pathname: string): "auth" | "login" | "app" | "error" {
	if (session.status === "error") return "error";
	if (isAuthPage(pathname)) return "auth";
	if (session.authMode === "hosted" && session.status === "ready" && !session.signedIn && !session.demo) return "login";
	return "app";
}