import "@/lib/i18n";

import { describe, expect, it, vi } from "vitest";
import { render } from "@/lib/testing/render";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

import { HeaderCrumbs, emergencyStopOn } from "./benchHeader";

// Wave 4, p16-1: a page under another sidebar entry ends the crumbs with its own title.
describe("HeaderCrumbs", () => {
	it("ends /connect with Connect an agent", () => {
		const html = render(<HeaderCrumbs pathname="/connect" />);
		expect(html).toContain("Connect an agent");
		expect(html).toContain('aria-current="page"');
	});
	it("ends a job page with Background job", () => {
		expect(render(<HeaderCrumbs pathname="/jobs/job_x" />)).toContain(">Background job<");
	});
	it("adds nothing on the entry's own page", () => {
		const html = render(<HeaderCrumbs pathname="/agents" />);
		expect(html).not.toContain("Connect an agent");
	});
});
// M8-57: the emergency stop banner shows only for a signed-in hosted visitor while the stop is on.
describe("emergencyStopOn", () => {
	it("is on only when hosted, signed in and the kill switch is set", () => {
		const hosted = { authMode: "hosted" as const, signedIn: true };
		expect(emergencyStopOn(hosted, { kill_switch: true })).toBe(true);
		expect(emergencyStopOn(hosted, { kill_switch: false })).toBe(false);
		expect(emergencyStopOn(hosted, undefined)).toBe(false);
		expect(emergencyStopOn({ authMode: "local", signedIn: false }, { kill_switch: true })).toBe(false);
		expect(emergencyStopOn({ ...hosted, signedIn: false }, { kill_switch: true })).toBe(false);
	});
});
