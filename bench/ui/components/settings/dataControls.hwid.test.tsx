import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { RetentionForm } from "./dataControls";

// Fix wave 3, L3: the retention setting names who last changed it. A member reads the display name
// only; an org admin also gets the email as the line's title.

// The unsaved-changes guard needs a router; static renders have none (M8-44).
vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));
vi.mock("@/lib/store/apis/dataOpsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/dataOpsApi")>()),
	useSetRetentionMutation: () => mutation(),
	useDeleteOrgMutation: () => mutation(),
}));

describe("RetentionForm editor", () => {
	it("a member sees the name and no email", () => {
		const html = render(
			<RetentionForm policy={{ run_days: 30, min_days: 1, max_days: 3650, updated_by_name: "Ada", updated_at: "2026-11-20T10:00:00Z" }} />,
			hostedSession("member"),
		);
		expect(html).toContain('data-testid="retention-changed-by"');
		expect(html).toContain("Last changed by Ada");
		expect(html).not.toContain("@");
	});
	it("an admin sees the name with the email as its title", () => {
		const html = render(
			<RetentionForm policy={{ run_days: 30, min_days: 1, max_days: 3650, updated_by: "ada@example.com", updated_by_name: "Ada" }} />,
			hostedSession("admin"),
		);
		expect(html).toContain("Last changed by Ada");
		expect(html).toContain('title="ada@example.com"');
	});
	it("says nothing when nobody changed it", () => {
		const html = render(<RetentionForm policy={{ run_days: 0, min_days: 1, max_days: 3650 }} />, hostedSession("member"));
		expect(html).not.toContain('data-testid="retention-changed-by"');
	});
});