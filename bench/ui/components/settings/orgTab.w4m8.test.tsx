import i18n from "@/lib/i18n";
import type { OrgInfo } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { togglePublic } from "./orgTab";

const info = { id: "org_a", slug: "acme", name: "Acme", public_read: false } as OrgInfo;

describe("public read card", () => {
	it("says public read is for people who are signed in", () => {
		const body = i18n.t("settings.org.publicBody", { ns: "platform" });
		expect(body).toContain("signed in");
		expect(body).not.toContain("without signing in");
		expect(i18n.t("settings.org.publicOn", { ns: "platform" })).toBe("Public to signed-in users");
	});

	it("asks before making the organization public", async () => {
		const setPublic = vi.fn();
		const no = vi.fn(() => Promise.resolve(false));
		await togglePublic(no, setPublic, info);
		expect(no).toHaveBeenCalledTimes(1);
		expect(no.mock.calls[0]).toEqual([
			"Make Acme public? Anyone signed in to this site can then read its results and evidence.",
			{ confirmLabel: "Make public" },
		]);
		expect(setPublic).not.toHaveBeenCalled();
		const yes = vi.fn(() => Promise.resolve(true));
		await togglePublic(yes, setPublic, info);
		expect(setPublic).toHaveBeenCalledWith({ orgId: "org_a", publicRead: true });
	});

	it("makes it private without asking", async () => {
		const setPublic = vi.fn();
		const ask = vi.fn(() => Promise.resolve(false));
		await togglePublic(ask, setPublic, { ...info, public_read: true });
		expect(ask).not.toHaveBeenCalled();
		expect(setPublic).toHaveBeenCalledWith({ orgId: "org_a", publicRead: false });
	});
});