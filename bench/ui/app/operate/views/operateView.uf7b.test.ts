import i18n from "@/lib/i18n";
import { describe, expect, it } from "vitest";
import { backupLabel } from "./operateView";

describe("backup copy label (M8-29)", () => {
	it("reads as saved results with date and size, no file name", () => {
		const t = i18n.getFixedT("en", "uf7b") as never;
		expect(backupLabel({ name: "evidence-org_a-20261121-100000.zip", size: 2048, mod_time: "2026-11-21T10:00:00Z" }, t)).toBe(
			"Saved results, 21 Nov 2026, 2.0 KiB",
		);
	});
});
