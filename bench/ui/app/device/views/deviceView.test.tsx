import "@/lib/i18n";

import { hostedSession, LOCAL_SESSION } from "@/lib/testing/platformFixtures";
import { errorQuery, loadingQuery, okQuery, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DeviceView, { isUserCode, normalizeUserCode, notACode } from "./deviceView";

const api = vi.hoisted(() => ({ device: {} as unknown, lookups: [] as { code: string; skip?: boolean }[] }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/deviceLoginApi", async () => {
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		useGetDeviceQuery: (code: string, opts?: { skip?: boolean }) => {
			api.lookups.push({ code, skip: opts?.skip });
			return api.device;
		},
		useApproveDeviceMutation: () => m({}),
		useDenyDeviceMutation: () => m({}),
	};
});

beforeEach(() => {
	api.device = okQuery({
		user_code: "BCDF-GHJK",
		client_name: "heimdall CLI on raj-laptop",
		client_ip: "203.0.113.7",
		scope: "evaluate",
		created_at: "2026-11-22T10:00:00Z",
		expires_at: "2099-01-01T00:00:00Z",
		status: "pending",
	});
});

describe("DeviceView", () => {
	it("shows the waiting login with what it may do, and approve and deny", () => {
		const html = render(<DeviceView initialCode="bcdf ghjk" />, hostedSession());
		expect(html).toContain('data-testid="device-pending"');
		expect(html).toContain("BCDF-GHJK");
		expect(html).toContain("heimdall CLI on raj-laptop");
		expect(html).toContain("Connect agents and run evaluations");
		expect(html).toContain('data-testid="device-approve"');
		expect(html).toContain('data-testid="device-deny"');
		expect(html).not.toContain("hm_");
	});
	it("asks for the code when the link has none", () => {
		const html = render(<DeviceView />, hostedSession());
		expect(html).toContain('data-testid="device-form"');
		expect(html).not.toContain('data-testid="device-pending"');
	});
	it("explains an expired or mistyped code and offers the form again", () => {
		api.device = errorQuery(404, "no login is waiting");
		const html = render(<DeviceView initialCode="XXXX-XXXX" />, hostedSession());
		expect(html).toContain('data-testid="device-not-found"');
		expect(html).toContain('data-testid="device-form"');
		api.device = loadingQuery();
		expect(render(<DeviceView initialCode="BCDF-GHJK" />, hostedSession())).toContain('data-testid="device-loading"');
	});
	it("tells a local server's user that no login is needed", () => {
		expect(render(<DeviceView initialCode="BCDF-GHJK" />, LOCAL_SESSION)).toContain('data-testid="device-local"');
	});
	it("normalizes typed codes", () => {
		expect(normalizeUserCode("bcdf ghjk")).toBe("BCDF-GHJK");
		expect(normalizeUserCode("BCDF-GHJK")).toBe("BCDF-GHJK");
		expect(normalizeUserCode("ab")).toBe("AB");
	});

	it("knows a sign-in code: 8 letters with no vowels (N146)", () => {
		expect(isUserCode("abc")).toBe(false);
		expect(isUserCode("bcdf ghjk")).toBe(true);
		expect(isUserCode("BCDF-GHJK")).toBe(true);
		expect(isUserCode("ABCD-EFGH")).toBe(false);
		expect(isUserCode("BCDF-GHJKL")).toBe(false);
		expect(notACode("abc")).toBe(false);
		expect(notACode("abcdefgh")).toBe(true);
		expect(notACode("bcdfghjk")).toBe(false);
	});
	it("refuses a linked code that cannot be one, before any lookup, and keeps Continue disabled", () => {
		api.lookups = [];
		const html = render(<DeviceView initialCode="abc" />, hostedSession());
		expect(html).toContain('data-testid="device-form"');
		expect(html).toContain("That is not a sign-in code. Codes are 8 letters with no vowels, like BCDF-GHJK.");
		expect(html).not.toContain('data-testid="device-not-found"');
		expect(html).toMatch(/<button[^>]*disabled=""[^>]*data-testid="device-find"|<button[^>]*data-testid="device-find"[^>]*disabled=""/);
		expect(api.lookups.every((l) => l.skip)).toBe(true);
		const vowels = render(<DeviceView initialCode="ABCD-EFGH" />, hostedSession());
		expect(vowels).toContain('data-testid="device-not-a-code"');
	});
});