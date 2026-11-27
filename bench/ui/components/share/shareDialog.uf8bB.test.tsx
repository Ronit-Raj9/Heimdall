import "@/lib/i18n";

import type { Share } from "@/lib/store/apis/shareApi";
import { describe, expect, it } from "vitest";
import { creatorName } from "./shareDialog";

const link = (extra: Partial<Share> & { created_by_name?: string }): Share =>
	({ id: "s1", kind: "evaluation", target: "e1", hide_prompts: false, created_at: "2026-11-01T00:00:00Z", state: "active", ...extra }) as Share;

describe("share link creator (lane 8b phase B, M8-72)", () => {
	it("shows the display name when the server sends it", () => {
		expect(creatorName(link({ created_by: "u_1", created_by_name: "Ana" }))).toBe("Ana");
	});
	it("keeps the stored id when the name is missing or empty (local mode)", () => {
		expect(creatorName(link({ created_by: "u_1" }))).toBe("u_1");
		expect(creatorName(link({ created_by: "u_1", created_by_name: " " }))).toBe("u_1");
		expect(creatorName(link({}))).toBe("");
	});
});
