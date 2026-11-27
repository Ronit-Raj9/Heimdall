import { describe, expect, it } from "vitest";
import { membersHaveActions } from "./membersTab";

describe("members Actions column (X-61)", () => {
	it("is hidden in a one-owner org", () => {
		expect(membersHaveActions([{ user_id: "u1", role: "owner" }], "u1", "owner", true, true)).toBe(false);
	});
	it("shows when the caller can manage another member", () => {
		expect(
			membersHaveActions(
				[
					{ user_id: "u1", role: "owner" },
					{ user_id: "u2", role: "member" },
				],
				"u1",
				"owner",
				true,
				true,
			),
		).toBe(true);
		expect(membersHaveActions([{ user_id: "u2", role: "member" }], "u1", "member", false, false)).toBe(false);
	});
});