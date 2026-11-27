import { describe, expect, it } from "vitest";
import { releaseOptionLabel } from "./releaseActions";

describe("releaseOptionLabel (M7-85)", () => {
	it("names the agent version and the day", () => {
		expect(releaseOptionLabel({ release_id: "rel_1", agent_id: "codex", version: 1, created_at: "2026-11-21T10:00:00Z" })).toBe("codex v1, 21 Nov 2026");
	});
	it("falls back to the release id", () => {
		expect(releaseOptionLabel({ release_id: "rel_1", created_at: "" })).toBe("rel_1");
	});
});
