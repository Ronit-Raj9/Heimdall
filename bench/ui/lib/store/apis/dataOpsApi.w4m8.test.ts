import { describe, expect, it } from "vitest";
import { auditExportUrl } from "./dataOpsApi";

describe("audit export URL", () => {
	it("carries the applied filters", () => {
		const url = auditExportUrl("org_a", "csv", { action: "member.role", actor: "token", q: "alpha beta" });
		expect(url).toContain("format=csv");
		expect(url).toContain("action=member.role");
		expect(url).toContain("actor_type=token");
		expect(url).toContain("q=alpha");
	});

	it("leaves unset filters out", () => {
		expect(auditExportUrl("org_a", "jsonl", { action: "", q: "  " })).toBe("/api/bench/orgs/org_a/audit/export?format=jsonl");
	});
});