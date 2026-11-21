import "@/lib/i18n";

import { LoadError } from "@/components/runs/screenStates";
import { demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import * as accessNotes from "./accessNotes";
import { ForbiddenNote, RoleGate, RoleNote } from "./accessNotes";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("no demo banner", () => {
	it("is gone: hosted installs serve no public demo", () => {
		expect("DemoBanner" in accessNotes).toBe(false);
	});
});

describe("ForbiddenNote and LoadError on 403", () => {
	it("says plainly that the role cannot do this, with the role needed and the server's reason", () => {
		const html = render(
			<ForbiddenNote min="admin" error={{ status: 403, data: { error: { message: "members need the admin role" } } }} />,
			hostedSession("viewer"),
		);
		expect(html).toContain("Your role cannot do this");
		expect(html).toContain("Your role in Acme Agents is viewer. This needs admin or above.");
		expect(html).toContain("members need the admin role");
	});

	it("turns a hosted 403 into the role note instead of an error with retry", () => {
		const error = { status: 403, data: { error: { message: "runners need the admin role" } } };
		const html = render(
			<LoadError title="Could not load runners" error={error} onRetry={() => undefined} testId="runners-error" />,
			hostedSession("member"),
		);
		expect(html).toContain("Your role cannot do this");
		expect(html).not.toContain("runners-error-retry");
	});

	it("keeps local mode's 403 (a recorded server refusing writes) as the plain error", () => {
		const error = { status: 403, data: { error: { message: "reviews are refused in recorded mode" } } };
		const html = render(<LoadError title="Could not save" error={error} onRetry={() => undefined} testId="save-error" />);
		expect(html).toContain("reviews are refused in recorded mode");
		expect(html).not.toContain("Your role cannot do this");
		expect(html).toContain("save-error-retry");
	});
});

describe("RoleGate and RoleNote", () => {
	const gated = (
		<RoleGate min="admin">
			<button type="button">invite</button>
		</RoleGate>
	);
	it("shows write controls in local mode and to roles that may use them", () => {
		expect(render(gated)).toContain("invite");
		expect(render(gated, hostedSession("admin"))).toContain("invite");
	});
	it("tells a signed-in member with too small a role what is needed", () => {
		const html = render(gated, hostedSession("member"));
		expect(html).not.toContain("invite");
		expect(html).toContain("Needs admin or above. Your role: member.");
	});
	it("shows a demo visitor nothing at all", () => {
		expect(render(gated, demoSession())).toBe("");
		expect(render(<RoleNote min="member" />, demoSession())).toContain("Sign in to use this.");
	});
});