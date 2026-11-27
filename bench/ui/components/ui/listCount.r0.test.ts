import i18n from "@/lib/i18n";
import { describe, expect, it } from "vitest";
import { listCountText } from "./pager";

const t = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

describe("listCountText (Q-56)", () => {
	it("names the hidden test items when every item is shown", () => {
		expect(listCountText(t, 59, 59, 21, "benchmarks")).toBe("59 benchmarks, 21 test items hidden");
		expect(listCountText(t, 59, 59, 1)).toBe("59 shown, 1 test item hidden");
	});
	it("keeps the page count otherwise", () => {
		expect(listCountText(t, 12, 80)).toBe("12 of 80");
		expect(listCountText(t, 12, 80, 3)).toBe("12 of 80 (3 hidden)");
	});
});
