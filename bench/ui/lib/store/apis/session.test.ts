import { afterEach, describe, expect, it, vi } from "vitest";
import {
	errorStatus,
	getAuthState,
	isAuthPage,
	isAuthRoute,
	isForbidden,
	isMissingRoute,
	isUnauthorized,
	notifyUnauthorized,
	onUnauthorized,
	roleAtLeast,
	safeReturnTo,
	setAuthState,
} from "./session";

afterEach(() => {
	setAuthState({ mode: "local" });
	onUnauthorized(null);
});

describe("safeReturnTo", () => {
	const cases: [unknown, string][] = [
		["/verdict?exp=e_1", "/verdict?exp=e_1"],
		["/invite/abc", "/invite/abc"],
		["/settings#tokens", "/settings#tokens"],
		["//evil.example/x", "/"],
		["/\\evil.example", "/"],
		["https://evil.example/", "/"],
		["javascript:alert(1)", "/"],
		["verdict", "/"],
		["/login?return_to=/x", "/"],
		["/signup", "/"],
		["/x\nSet-Cookie: a", "/"],
		[undefined, "/"],
		[42, "/"],
		["", "/"],
	];
	for (const [raw, want] of cases) {
		it(`${JSON.stringify(raw)} -> ${want}`, () => expect(safeReturnTo(raw)).toBe(want));
	}
});

describe("roles", () => {
	it("ranks viewer < member < admin < owner and refuses unknown roles", () => {
		expect(roleAtLeast("owner", "admin")).toBe(true);
		expect(roleAtLeast("admin", "admin")).toBe(true);
		expect(roleAtLeast("member", "admin")).toBe(false);
		expect(roleAtLeast("viewer", "member")).toBe(false);
		expect(roleAtLeast(undefined, "viewer")).toBe(false);
		expect(roleAtLeast("root" as never, "viewer")).toBe(false);
	});
});

describe("error statuses", () => {
	it("reads the status of RTK Query errors, including unparsable bodies", () => {
		expect(errorStatus({ status: 403, data: {} })).toBe(403);
		expect(errorStatus({ status: "PARSING_ERROR", originalStatus: 502, data: "x", error: "y" })).toBe(502);
		expect(errorStatus({ status: "FETCH_ERROR", error: "offline" })).toBeUndefined();
		expect(errorStatus(undefined)).toBeUndefined();
		expect(isForbidden({ status: 403 })).toBe(true);
		expect(isUnauthorized({ status: 401 })).toBe(true);
		expect(isMissingRoute({ status: 404 })).toBe(true);
		expect(isForbidden({ status: 401 })).toBe(false);
	});
});

describe("auth routes and pages", () => {
	it("tells auth routes from bench routes", () => {
		expect(isAuthRoute("/auth/me")).toBe(true);
		expect(isAuthRoute("/api/bench/auth/login")).toBe(true);
		expect(isAuthRoute("/experiments")).toBe(false);
		expect(isAuthRoute("/experiments?auth/x")).toBe(false);
	});
	it("knows the pages that render without the shell", () => {
		expect(isAuthPage("/login")).toBe(true);
		expect(isAuthPage("/signup")).toBe(true);
		expect(isAuthPage("/invite/tok")).toBe(true);
		expect(isAuthPage("/settings")).toBe(false);
	});
});

describe("notifyUnauthorized", () => {
	it("fires only in hosted mode and never for auth routes", () => {
		const listener = vi.fn();
		onUnauthorized(listener);
		notifyUnauthorized("/experiments");
		expect(listener).not.toHaveBeenCalled(); // local mode: a 401 is the write-token check
		setAuthState({ mode: "hosted", csrf: "c" });
		notifyUnauthorized("/auth/login");
		expect(listener).not.toHaveBeenCalled();
		notifyUnauthorized("/experiments");
		expect(listener).toHaveBeenCalledTimes(1);
		expect(getAuthState()).toEqual({ mode: "hosted", csrf: "c" });
	});
});