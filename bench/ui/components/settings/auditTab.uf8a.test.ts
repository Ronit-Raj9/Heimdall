import "@/lib/i18n";
import i18n from "@/lib/i18n";
import type { AuditEntry } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { auditAbsolute, auditGroupOf, auditSentence, filterAuditView, isReadAction } from "./auditTab";

const row = (action: string, target_id = ""): AuditEntry =>
	({ id: action, action, actor_type: "user", actor_id: "u", target_type: "", target_id, at: "2026-11-20T10:00:00Z" }) as AuditEntry;
const t = i18n.t.bind(i18n) as unknown as Parameters<typeof auditSentence>[0];

describe("activity log (lane 8a)", () => {
	it("reads entries as sentences, raw id as fallback", () => {
		expect(auditSentence(t, row("runs.diff.read", "r_1"), "Ada")).toBe("Ada opened runs diff r_1");
		expect(auditSentence(t, row("member.role", "usr_grace"), "Ada")).toBe("Ada changed the role of usr_grace");
		expect(auditSentence(t, row("odd.thing"), "Ada")).toBe("Ada: odd.thing");
	});
	it("hides reads unless asked and filters by group", () => {
		const rows = [row("runs.diff.read"), row("member.role"), row("experiment.create")];
		expect(isReadAction("runs.diff.read")).toBe(true);
		expect(filterAuditView(rows, { reads: false, group: "all" }).map((r) => r.action)).toEqual(["member.role", "experiment.create"]);
		expect(filterAuditView(rows, { reads: true, group: "runs" }).map((r) => r.action)).toEqual(["runs.diff.read", "experiment.create"]);
		expect(auditGroupOf("member.role")).toBe("members");
	});
	it("shows the absolute time after a day", () => {
		const now = Date.parse("2026-11-27T10:00:00Z");
		expect(auditAbsolute("2026-11-20T10:00:00Z", now)).toBe(true);
		expect(auditAbsolute("2026-11-27T09:00:00Z", now)).toBe(false);
	});
});