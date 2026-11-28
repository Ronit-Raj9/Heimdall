import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import { signedOutSession } from "@/lib/testing/platformFixtures";
import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { describe, expect, it, vi } from "vitest";
import { IncidentAdmin, incidentBody, noteBody } from "./incidentForm";
import StatusView from "./statusView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/statusApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/statusApi")>()),
	useGetStatusPageQuery: () =>
		okQuery({ level: "operational", checked_at: "2026-11-28T10:00:00Z", components: [], incidents: [], recent: [] }),
}));

vi.mock("@/lib/store/apis/statusIncidentsApi", () => ({
	useOpenIncidentMutation: () => [vi.fn(), { isLoading: false }],
	useUpdateIncidentMutation: () => [vi.fn(), { isLoading: false }],
}));

describe("incident form", () => {
	// handlers/statusincidents_test.go posts these exact bodies to the Go handler.
	it("sends the bodies the server reads", () => {
		expect(JSON.stringify(incidentBody(" Runs start late ", "Looking into it. ", "major"))).toBe(
			'{"title":"Runs start late","message":"Looking into it.","impact":"major"}',
		);
		expect(JSON.stringify(noteBody("Fixed.", "resolved"))).toBe('{"message":"Fixed.","status":"resolved"}');
	});

	it("shows only to server admins", () => {
		expect(render(<StatusView />, signedOutSession())).not.toContain("status-incident-admin");
		expect(render(<StatusView />, { ...LOCAL_SESSION, serverAdmin: true })).toContain("status-incident-admin");
	});

	it("says so when nothing is open, else lists the open incidents", () => {
		expect(render(<IncidentAdmin open={[]} onSaved={() => {}} />)).toContain("No open incidents.");
		const html = render(
			<IncidentAdmin
				open={[
					{
						id: "inc_1",
						title: "Runs start late",
						status: "investigating",
						impact: "major",
						started_at: "2026-11-28T09:00:00Z",
						updates: [],
					},
				]}
				onSaved={() => {}}
			/>,
		);
		expect(html).toContain("Runs start late");
		expect(html).toContain("Add note");
	});
});