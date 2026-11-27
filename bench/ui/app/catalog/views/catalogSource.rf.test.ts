import "@/lib/i18n";
import { describe, expect, it } from "vitest";
import type { CatalogEntry } from "@/lib/types/catalog";
import { sourceKeyOf } from "./catalogView";

describe("catalog Source cell (Q-197)", () => {
	const builtin = new Set(["hd-harbor-skills"]);
	it("a built-in benchmark never reads Needs data", () => {
		const e = { id: "skillsbench", needs_data: true, suite_id: "hd-harbor-skills" } as unknown as CatalogEntry;
		expect(sourceKeyOf(e, builtin)).toBe("sourceReady");
	});
	it("an entry without a built-in suite that needs data says so", () => {
		const e = { id: "x", needs_data: true } as unknown as CatalogEntry;
		expect(sourceKeyOf(e, builtin)).toBe("sourceNeedsData");
	});
});
