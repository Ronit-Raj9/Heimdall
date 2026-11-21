import { AUTH_CONFIG, ME_LOCAL, ME_OWNER, demoSession, hostedSession, signedOutSession } from "@/lib/testing/platformFixtures";
import { afterEach, describe, expect, it } from "vitest";
import { setAuthState } from "@/lib/store/apis/session";
import { LOCAL_SESSION, canUse, frameFor, sessionFromMe, usesWriteToken } from "./sessionContext";

afterEach(() => setAuthState({ mode: "local" }));

describe("sessionFromMe", () => {
	it("is local mode for auth_mode local and for a server without /auth/me (404)", () => {
		expect(sessionFromMe({ me: ME_LOCAL, meLoading: false })).toEqual(LOCAL_SESSION);
		expect(sessionFromMe({ meError: { status: 404, data: {} }, meLoading: false })).toEqual(LOCAL_SESSION);
	});

	// Audit qc2-16-05: a transport error or a 5xx before any answer is not proof of local mode.
	it("offers a retry, never local controls, when /auth/me fails with neither 401 nor 404", () => {
		for (const meError of [{ status: "FETCH_ERROR", error: "x" }, { status: 502, data: {} }]) {
			const s = sessionFromMe({ meError, meLoading: false });
			expect(s).toMatchObject({ status: "error", authMode: "hosted", signedIn: false });
			expect(canUse(s, "viewer")).toBe(false);
			expect(usesWriteToken(s)).toBe(false);
			expect(frameFor(s, "/verdict")).toBe("error");
		}
		// Once hosted is known, a failed reload keeps the last answer.
		expect(sessionFromMe({ me: ME_OWNER, meError: { status: 503, data: {} }, meLoading: false })).toMatchObject({ status: "ready", signedIn: true });
	});

	it("reads a signed-in user: current org, role, every org for the switcher", () => {
		const s = sessionFromMe({ me: ME_OWNER, meLoading: false, config: AUTH_CONFIG });
		expect(s).toMatchObject({ authMode: "hosted", kind: "user", signedIn: true, role: "owner", status: "ready" });
		expect(s.org).toMatchObject({ id: "org_acme", name: "Acme Agents" });
		expect(s.orgs.map((o) => o.slug)).toEqual(["acme", "labs"]);
		expect(s.github).toBe(true);
	});

	it("is a signed-out hosted visitor on 401, never a demo reader", () => {
		expect(signedOutSession()).toMatchObject({ authMode: "hosted", signedIn: false, kind: "anonymous", role: undefined });
		expect(signedOutSession()).not.toHaveProperty("demo");
		expect(demoSession()).toEqual(signedOutSession());
	});

	it("lets a 401 win over earlier data: the session ended", () => {
		const s = sessionFromMe({ me: ME_OWNER, meError: { status: 401, data: {} }, meLoading: false });
		expect(s.signedIn).toBe(false);
	});

	it("stays loading while the demo read is on its way, so the shell does not bounce to login", () => {
		const s = sessionFromMe({ meError: { status: 401, data: {} }, meLoading: true });
		expect(s.status).toBe("loading");
		expect(frameFor(s, "/verdict")).toBe("app");
	});

	it("keeps hosted mode while /auth/me reloads once the request layer knows it", () => {
		setAuthState({ mode: "hosted", csrf: "" });
		expect(sessionFromMe({ meLoading: true })).toMatchObject({ status: "loading", authMode: "hosted" });
		setAuthState({ mode: "local" });
		expect(sessionFromMe({ meLoading: true })).toMatchObject({ status: "loading", authMode: "local" });
	});
});

describe("canUse", () => {
	it("always allows in local mode: the write token is the gate", () => {
		expect(canUse(LOCAL_SESSION, "owner")).toBe(true);
		expect(usesWriteToken(LOCAL_SESSION)).toBe(true);
	});
	it("follows the role in hosted mode and never allows a demo visitor", () => {
		expect(canUse(hostedSession("viewer"), "member")).toBe(false);
		expect(canUse(hostedSession("member"), "member")).toBe(true);
		expect(canUse(hostedSession("member"), "admin")).toBe(false);
		expect(canUse(hostedSession("admin"), "admin")).toBe(true);
		expect(canUse(hostedSession("admin"), "owner")).toBe(false);
		expect(canUse(demoSession(), "viewer")).toBe(false);
		expect(usesWriteToken(hostedSession())).toBe(false);
	});
});

describe("frameFor", () => {
	it("renders auth pages bare, sends signed-out hosted visitors to login, and leaves local mode alone", () => {
		expect(frameFor(LOCAL_SESSION, "/verdict")).toBe("app");
		expect(frameFor(LOCAL_SESSION, "/login")).toBe("auth");
		expect(frameFor(signedOutSession(), "/verdict")).toBe("login");
		expect(frameFor(signedOutSession(), "/invite/tok")).toBe("auth");
		expect(frameFor(signedOutSession(), "/s/org_a.shr_1.sig")).toBe("share");
		expect(frameFor({ ...signedOutSession(), status: "error" }, "/s/org_a.shr_1.sig")).toBe("share");
		expect(frameFor(demoSession(), "/verdict")).toBe("login");
		expect(frameFor(hostedSession("viewer"), "/settings")).toBe("app");
	});
});