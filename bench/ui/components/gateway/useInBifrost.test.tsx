import "@/lib/i18n";

import { render, tagOf } from "@/lib/testing/render";
import type { GwSetting } from "@/lib/types/gateway";
import { describe, expect, it, vi } from "vitest";
import { UseInBifrostBody, UseInBifrostButton, copyText } from "./useInBifrost";

const setting: GwSetting = {
	kind: "routing_rule",
	title: "Routing rule heimdall-e1",
	where: "Bifrost dashboard, Routing rules",
	body: '{"name": "heimdall-e1", "enabled": false}',
	note: "The rule is created switched off.",
};

describe("Use this in Bifrost", () => {
	it("copies the body", async () => {
		const writeText = vi.fn(() => Promise.resolve());
		expect(await copyText(setting.body, { writeText })).toBe(true);
		expect(writeText).toHaveBeenCalledWith(setting.body);
		expect(await copyText("x", { writeText: () => Promise.reject(new Error("denied")) })).toBe(false);
		expect(await copyText("x", undefined)).toBe(false);
	});

	it("shows title, where, the body, the note and the fixed line", () => {
		const html = render(<UseInBifrostBody setting={setting} copied={false} onCopy={() => undefined} />);
		expect(html).toContain("Routing rule heimdall-e1");
		expect(html).toContain("Where: Bifrost dashboard, Routing rules");
		expect(html).toContain("heimdall-e1&quot;, &quot;enabled&quot;: false");
		expect(html).toContain(">Copy</button>");
		expect(html).toContain("The rule is created switched off.");
		expect(html).toContain("Heimdall does not change your Bifrost. Paste this where it says, review it, then switch it on.");
	});

	it("is disabled with the reason when there is no setting", () => {
		const reason = "The comparison could not tell the two setups apart, so there is no setting to change.";
		const html = render(<UseInBifrostButton setting={null} reason={reason} />);
		expect(tagOf(html, "use-in-bifrost")).toContain(`disabled=""`);
		expect(tagOf(html, "use-in-bifrost-wrap")).toContain(`title="${reason}"`);
		expect(html).toContain(reason);
		expect(tagOf(render(<UseInBifrostButton setting={setting} />), "use-in-bifrost")).not.toContain(`disabled=""`);
	});
});