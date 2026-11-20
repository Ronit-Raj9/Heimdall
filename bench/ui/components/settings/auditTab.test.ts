import { auditLogRequest } from "@/lib/store/apis/platformApi";
import { describe, expect, it } from "vitest";
import { nextAuditCursor } from "./auditTab";

describe("audit paging", () => {
	it("follows the server's opaque cursor, not a row timestamp", () => {
		expect(nextAuditCursor({ entries: [], next: "MTcwMDow" })).toBe("MTcwMDow");
		expect(nextAuditCursor({ entries: [] })).toBeNull();
		expect(nextAuditCursor(undefined)).toBeNull();
	});

	it("sends the cursor as cursor=", () => {
		expect(auditLogRequest({ orgId: "org_a", cursor: "abc", limit: 50 })).toEqual({
			url: "/orgs/org_a/audit",
			params: { cursor: "abc", limit: 50 },
		});
		expect(auditLogRequest({ orgId: "org_a" })).toBe("/orgs/org_a/audit");
	});
});
