import "@/lib/i18n";

import { mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { LiveSetup, liveInitParams, liveSetupCanSave } from "./liveActions";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/hooks", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/hooks")>()),
	useAppDispatch: () => () => undefined,
}));
vi.mock("@/lib/store/apis/jobRunApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/jobRunApi")>()),
	useCreateJobMutation: () => mutation(),
}));

/** The opening tag of the element with this data-testid. */
function tag(html: string, testId: string): string {
	return html.match(new RegExp(`<[a-z]+[^>]*data-testid="${testId}"[^>]*>`))?.[0] ?? "";
}

const v = { url: "", token_env: "", baseline_key: "vk-a", candidate_key: "vk-b", tag_header: "x-bf-lh-agent-version", baseline_tag: "", candidate_tag: "" };

describe("liveInitParams", () => {
	it("sends the keys split", () => {
		expect(liveInitParams("keys", v, false)).toEqual({ force: false, baseline_key: "vk-a", candidate_key: "vk-b" });
	});
	it("needs both tag values", () => {
		expect(liveInitParams("tag", v, false)).toBeNull();
		expect(liveInitParams("tag", { ...v, baseline_tag: "v1", candidate_tag: "v2" }, true)).toEqual({
			force: true,
			tag_header: "x-bf-lh-agent-version",
			baseline_tag: "v1",
			candidate_tag: "v2",
		});
	});
	it("writes a template when deciding later, with the optional gateway fields trimmed", () => {
		expect(liveInitParams("later", { ...v, url: " http://gw:8080 ", token_env: "BF_TOKEN" }, false)).toEqual({
			force: false,
			url: "http://gw:8080",
			token_env: "BF_TOKEN",
		});
	});
});

describe("LiveSetup (N131)", () => {
	it("starts with Replace unticked on a fresh organization, with no note about existing settings", () => {
		const html = render(<LiveSetup />);
		expect(tag(html, "live-setup-force")).not.toContain("checked");
		expect(html).not.toContain('data-testid="live-setup-existing"');
	});

	it("keeps Replace unticked when settings exist, says so, and holds Save until it is ticked", () => {
		const html = render(<LiveSetup existing />);
		expect(tag(html, "live-setup-force")).not.toContain("checked");
		expect(html).toContain("This organization already has live scoring settings. Tick Replace to overwrite them.");
		expect(tag(html, "live-setup-submit")).toContain("disabled");
		const later = liveInitParams("later", v, false);
		expect(liveSetupCanSave(later, true, false)).toBe(false);
		expect(liveSetupCanSave(later, true, true)).toBe(true);
		expect(liveSetupCanSave(later, false, false)).toBe(true);
		expect(liveSetupCanSave(null, false, true)).toBe(false);
	});
});
