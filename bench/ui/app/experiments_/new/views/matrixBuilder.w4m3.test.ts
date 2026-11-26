import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

describe("setup count in the comparison preview (p06-7)", () => {
	it("says 1 setup and 3 setups", () => {
		expect(i18n.t("preview.arms", { ns: "w4m3", count: 1 })).toBe("1 setup");
		expect(i18n.t("preview.arms", { ns: "w4m3", count: 3 })).toBe("3 setups");
	});
	it("the preview uses the plural key", () => {
		const src = readFileSync(new URL("./matrixBuilder.tsx", import.meta.url), "utf8");
		expect(src).toContain('t("preview.arms", { ns: "w4m3", count: preview.arms.length })');
	});
});