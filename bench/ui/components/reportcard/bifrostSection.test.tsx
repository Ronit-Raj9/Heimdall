import type { Report } from "@/lib/store/apis/evaluationsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BifrostTable, hasBifrost } from "./bifrostSection";

describe("BifrostTable", () => {
	it("shows one row per agent and dashes for numbers no run observed", () => {
		const report = {
			mode: "score",
			arms: [],
			bifrost: [{ arm: "A", runs: 4, fallback_runs: 1, fallback_known: 3, cache_hits: 5, input_tokens: 12000, cost_usd: 0.25, cost_runs: 2 }],
		} as unknown as Report;
		expect(hasBifrost(report)).toBe(true);
		const html = renderToStaticMarkup(<BifrostTable report={report} />);
		expect(html).toContain('data-testid="report-bifrost"');
		expect(html).toContain("1 of 3");
		expect(html).toContain("12,000");
		expect(html).toContain("$0.2500");
		expect(html).toContain("2 of 4 runs");
		expect(html).toContain(">-<");
		expect(hasBifrost({ arms: [] } as unknown as Report)).toBe(false);
	});
});
