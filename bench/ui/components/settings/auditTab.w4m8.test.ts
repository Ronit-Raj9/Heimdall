import i18n from "@/lib/i18n";
import type { AuditEntry } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { actorTypeLabel, auditActorName } from "./auditTab";

const t = i18n.getFixedT("en", "platform") as unknown as Parameters<typeof actorTypeLabel>[0];

describe("audit actor labels", () => {
	it("names the anonymous actor in plain words and never shows a key path", () => {
		expect(actorTypeLabel(t, "anonymous")).toBe("Anonymous visitor");
		expect(actorTypeLabel(t, "token")).toBe("Token");
		expect(actorTypeLabel(t, "robot")).toBe("robot");
	});

	it("shows the member's name, never an address", () => {
		const row = {
			id: "a1",
			actor_type: "user",
			actor_id: "u_1",
			action: "x",
			at: "2026-11-01T00:00:00Z",
		} as AuditEntry;
		const members = [{ user_id: "u_1", email: "ana@example.com", name: "Ana" }];
		expect(auditActorName(t, row, members)).toBe("Ana");
		const noName = [{ user_id: "u_1", email: "ana@example.com" }];
		const nameless = auditActorName(t, row, noName);
		expect(nameless).toBe("Someone");
		expect(nameless).not.toContain("@");
		expect(auditActorName(t, { ...row, actor_id: "u_9" }, members)).toBe("Someone");
	});
});