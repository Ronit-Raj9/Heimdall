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
		// M6-76: the runs line is one plural sentence counted by runs ("2 setups x ... = 10 runs at most").
		const src = readFileSync(new URL("./matrixBuilder.tsx", import.meta.url), "utf8");
		expect(src).toContain("count: preview.maxRuns,");
		expect(i18n.t("preview.runs", { ns: "builder", count: 10, arms: 2, conditions: 1, tasks: 5, trials: 1 })).toBe(
			"2 setups x 1 tests x 5 tasks x 1 tries = 10 runs at most",
		);
	});
});