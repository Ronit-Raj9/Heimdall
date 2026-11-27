import "@/lib/i18n";

import type { Share } from "@/lib/store/apis/shareApi";
import i18n from "@/lib/i18n";
import { describe, expect, it } from "vitest";
import { shareAudienceLines, splitLinks } from "./shareDialog";
import "./uf8bI18n";

const link = (id: string, state: Share["state"]): Share => ({ id, kind: "evaluation", target: "e1", hide_prompts: false, created_at: "2026-11-01T00:00:00Z", state });

describe("share dialog (lane 8b)", () => {
	it("says what an outsider sees, that the page is live, and that the page address needs an account (M8-69)", () => {
		const lines = shareAudienceLines("evaluation", false).map((k) => i18n.t(k, { ns: "uf8b" }));
		expect(lines).toHaveLength(4);
		expect(lines[0]).toContain("They will see");
		expect(lines[1]).toContain("Never shown");
		expect(lines[1]).not.toContain("prompts");
		expect(lines[2]).toContain("live page");
		expect(lines[3]).toContain("needs an account");
		expect(i18n.t(shareAudienceLines("verdict", true)[1], { ns: "uf8b" })).toContain("prompts and agent text");
	});

	it("lists active links and folds the rest (M8-30)", () => {
		const { active, off } = splitLinks([link("a", "active"), link("b", "revoked"), link("c", "expired")]);
		expect(active.map((s) => s.id)).toEqual(["a"]);
		expect(off.map((s) => s.id)).toEqual(["b", "c"]);
	});
});
