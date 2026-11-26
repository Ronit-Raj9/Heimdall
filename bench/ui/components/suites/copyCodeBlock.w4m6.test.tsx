import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { copiedAnnouncement } from "./copyCodeBlock";

describe("copy label (p11-7c)", () => {
	it("says Code copied for code and Command copied for a command", () => {
		expect(copiedAnnouncement("code")).toBe("Code copied to the clipboard");
		expect(copiedAnnouncement("command")).toBe("Command copied to the clipboard");
		expect(copiedAnnouncement(undefined)).toBe("Command copied to the clipboard");
	});
});