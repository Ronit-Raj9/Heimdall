import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { ImprovementEstimate } from "@/lib/types/improve";
import { describe, expect, it } from "vitest";
import { EstimateCard } from "./estimateCard";

const base: ImprovementEstimate = {
	ok: true,
	pool: 120,
	splits: { practice: 48, gate: 48, held_out: 24, impossible: 0 },
	runs: { baseline: 48, improver: 6, practice: 288, gate: 576, held_out_curve: 0, final: 96, total: 1008 },
	usd: { low: 10, expected: 14, high: 30, limit: 30, per_run: 0.02, per_run_source: "forecast" },
	minutes: { low: 10, high: 20 },
	detectable: { gate_points: 12, held_out_points: 15 },
	assumed: [],
	refusals: [],
	warnings: [],
};

describe("EstimateCard study limit", () => {
	it("shows the study's total and each loop's share", () => {
		const html = render(<EstimateCard estimate={{ ...base, loops: 3, usd: { ...base.usd, loop_limit: 10 } }} model="m" />);
		expect(html).toContain("Total for 3 loops: at most $30.00 ($10.00 for each loop)");
	});

	it("shows no study line for one run", () => {
		const html = render(<EstimateCard estimate={base} model="m" />);
		expect(html).not.toContain('data-testid="estimate-study-limit"');
	});
});