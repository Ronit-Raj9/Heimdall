import { afterEach, describe, expect, it } from "vitest";
import { auditExportUrl } from "./dataOpsApi";
import { setAuthState } from "./session";

// Wave 4, organization per tab: the audit export is a download the browser opens directly, so it names the tab's organization in hosted mode.
afterEach(() => setAuthState({ mode: "local" }));

describe("audit export link", () => {
	it("carries the tab's organization in hosted mode and nothing in local mode", () => {
		setAuthState({ mode: "hosted", csrf: "c", org: "org_a" });
		expect(auditExportUrl("org_a", "csv", { action: "member.add" })).toBe(
			"/api/bench/orgs/org_a/audit/export?format=csv&action=member.add&heimdall_org=org_a",
		);
		setAuthState({ mode: "local" });
		expect(auditExportUrl("org_a", "csv")).toBe("/api/bench/orgs/org_a/audit/export?format=csv");
	});
});