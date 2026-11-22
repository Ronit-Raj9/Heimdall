import "@/lib/i18n";

import { hostedSession, LOCAL_SESSION } from "@/lib/testing/platformFixtures";
import { errorQuery, loadingQuery, okQuery, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DeviceView, { normalizeUserCode } from "./deviceView";

const api = vi.hoisted(() => ({ device: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/deviceLoginApi", async () => {
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		useGetDeviceQuery: () => api.device,
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
});
