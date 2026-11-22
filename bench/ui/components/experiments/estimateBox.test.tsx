import "@/lib/i18n";

import type { ArmForecast, Forecast, ForecastFit, ForecastRange } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { forecastFit, forecastRangeView } from "./estimate.utils";
import { EstimateBox } from "./estimateBox";

const history = (low: number, median: number, high: number): ForecastRange => ({ kind: "history", low, median, high });
const upper = (high: number): ForecastRange => ({ kind: "upper_bound", low: null, median: null, high });
const unknown = (reason?: string): ForecastRange => ({
	kind: "unknown",
	low: null,
	median: null,
	high: null,
	...(reason ? { reason } : {}),
});

const ARM_A: ArmForecast = {
	arm: "A",
	label: "A: baseline",
	agent: "codex",
	model: "gpt-5.4-mini",
	config_hash: "sha256:3f2a91c0",
	runs: 24,
	basis: "config_hash",
	basis_text: "24 sealed runs of config 3f2a91c0 in 2 experiments",
	history_runs: 24,
	experiments: ["e_1", "e_2"],
	task_level: 4,
	per_run: { cost_usd: history(0.09, 0.125, 0.18), tokens: history(28000, 41000, 66000), wall_s: history(120, 184, 300) },
	cost_usd: history(2.16, 3, 4.32),
	tokens: history(672000, 984000, 1584000),
	agent_time_s: history(2880, 4416, 7200),
};

const BASE: Forecast = {
	exp_id: "e_new",
	suite: "coding-core@1.0.0",
	generated_at: "2026-11-15T10:20:00Z",
	arms: [ARM_A],
	cost_usd: history(4.8, 6.84, 10.56),
	agent_time_s: history(6240, 9456, 15360),
	wall_clock_s: history(1560, 2364, 3840),
	concurrency: 4,
	budget_usd: 20,
	fit: "fits",
	fit_reason: "Even the high end $10.56 is within the $20.00 budget.",
	basis: "Every arm from sealed runs of its own config.",
	notes: [],
};

const render = (forecast: Forecast) => renderToStaticMarkup(<EstimateBox forecast={forecast} />);
/** The markup of the element with this test id, up to its first closing span. */
const piece = (html: string, testId: string) => html.match(new RegExp(`data-testid="${testId}"[^>]*>([^<]*)<`))?.[1] ?? null;

describe("forecastRangeView", () => {
	it("formats each kind and never turns a null into 0", () => {
		expect(forecastRangeView(history(1, 2, 3), "usd", "en")).toEqual({ kind: "history", low: "$1.00", median: "$2.00", high: "$3.00" });
		expect(forecastRangeView(upper(7200), "duration", "en")).toEqual({ kind: "upper_bound", high: "2h" });
		expect(forecastRangeView(unknown("no price"), "usd", "en")).toEqual({ kind: "unknown", reason: "no price" });
		expect(forecastRangeView(unknown(), "usd", "en")).toEqual({ kind: "unknown", reason: null });
		// A history range missing a number, or an upper bound without its high, is unknown.
		expect(forecastRangeView({ kind: "history", low: 1, median: null, high: 3 }, "usd", "en").kind).toBe("unknown");
		expect(forecastRangeView({ kind: "upper_bound", low: null, median: null, high: null }, "usd", "en").kind).toBe("unknown");
		expect(forecastRangeView(undefined, "usd", "en")).toEqual({ kind: "unknown", reason: null });
	});

	it("reads an unknown fit as unknown", () => {
		expect(forecastFit("may_exceed")).toBe("may_exceed");
		expect(forecastFit("something-new")).toBe("unknown");
	});
});

describe("EstimateBox", () => {
	it("shows history ranges as low to high with the median, and seconds as hours and minutes", () => {
		const html = render(BASE);
		expect(piece(html, "estimate-cost-value")).toBe("$4.80 to $10.56 (median $6.84)");
		expect(piece(html, "estimate-agent-time-value")).toBe("1.7h to 4.3h (median 2.6h)");
		expect(piece(html, "estimate-wall-clock-value")).toBe("26m to 1.1h (median 39.4m)");
		expect(html).toContain("Agent time over a concurrency of 4, no queueing assumed.");
		expect(html).toContain('data-testid="estimate-basis"');
		expect(html).toContain("Every arm from sealed runs of its own config.");
		expect(piece(html, "estimate-arm-cost-A")).toBe("$2.16 to $4.32 (median $3.00)");
		expect(html).toContain("24 sealed runs of config 3f2a91c0 in 2 experiments");
		expect(html).toContain('data-basis="config_hash"');
		expect(html).not.toContain('data-testid="estimate-judge"');
		expect(html).not.toContain('data-testid="estimate-notes"');
	});

	it("shows an upper bound as at most the high end", () => {
		const html = render({ ...BASE, cost_usd: upper(16.32), fit: "may_exceed", fit_reason: "At most $16.32." });
		expect(piece(html, "estimate-cost-value")).toBe("at most $16.32");
		expect(html).toMatch(/data-testid="estimate-cost-value" data-kind="upper_bound"/);
	});

	it("shows the reason of an unknown range, or the default words, and never $0", () => {
		const html = render({
			...BASE,
			cost_usd: unknown("no price for model x"),
			wall_clock_s: unknown(),
			arms: [{ ...ARM_A, cost_usd: { kind: "history", low: 1, median: null, high: 2 } }],
			fit: "unknown",
		});
		expect(piece(html, "estimate-cost-value")).toBe("no price for model x");
		expect(piece(html, "estimate-wall-clock-value")).toBe("no history, cannot estimate");
		expect(piece(html, "estimate-arm-cost-A")).toBe("no history, cannot estimate");
		expect(html).not.toContain("$0");
	});

	// N41: with no history the cost says so plainly and names the budget that caps it; no made-up bound.
	it("explains an unknown cost with no history and says the budget caps it", () => {
		const html = render({
			...BASE,
			cost_usd: unknown("arm B has no history and no per-run budget"),
			agent_time_s: upper(9000),
			wall_clock_s: upper(2250),
			arms: [{ ...ARM_A, basis: "none", basis_text: "no history", cost_usd: unknown("no history") }],
			fit: "unknown",
			price_ref: "bifrost-datasheet-2026-11-14-openai",
		});
		expect(piece(html, "estimate-cost-value")).toBe(
			"Not known yet: nothing like this has run on this benchmark, so there is no history to estimate from.",
		);
		expect(piece(html, "estimate-arm-cost-A")).toBe("Not known yet (no history)");
		expect(piece(html, "estimate-budget-cap")).toBe("The budget of $20.00 stops the comparison when it is spent.");
		expect(piece(html, "estimate-agent-time-value")).toBe("at most 2.5h (from each agent&#x27;s time limit)");
		expect(piece(html, "estimate-wall-clock-value")).toBe("at most 37.5m (from each agent&#x27;s time limit)");
		expect(piece(html, "estimate-price-ref")).toBe(
			"Model prices come from bifrost-datasheet-2026-11-14-openai. They are used once there is history to price.",
		);
		expect(html).not.toContain("Prices checked against");
		expect(html).not.toContain("no history, cannot estimate");
	});

	it("labels every fit and tones it", () => {
		const cases: { fit: ForecastFit; label: string; tone: string }[] = [
			{ fit: "fits", label: "Fits the budget", tone: "text-chart-success-ink" },
			{ fit: "may_exceed", label: "May exceed the budget", tone: "text-chart-warning-ink" },
			{ fit: "likely_exceeds", label: "Likely exceeds the budget", tone: "text-chart-error-ink" },
			{ fit: "exceeds", label: "Exceeds the budget", tone: "text-chart-error-ink" },
			{ fit: "unknown", label: "Fit unknown", tone: "text-muted-foreground" },
			{ fit: "no_budget", label: "No budget set", tone: "text-muted-foreground" },
		];
		for (const c of cases) {
			const html = render({ ...BASE, fit: c.fit, fit_reason: `reason for ${c.fit}` });
			expect(html).toContain(`data-fit="${c.fit}"`);
			expect(piece(html, "estimate-fit")).toBe(c.label);
			expect(html.match(/<span class="([^"]*)" data-testid="estimate-fit"/)?.[1]).toContain(c.tone);
			expect(piece(html, "estimate-fit-reason")).toBe(`reason for ${c.fit}`);
		}
	});

	it("shows the budget only when one is set", () => {
		expect(piece(render(BASE), "estimate-budget")).toBe("Budget $20.00");
		expect(render({ ...BASE, budget_usd: 0, fit: "no_budget" })).not.toContain('data-testid="estimate-budget"');
	});

	it("shows the judge line, notes and price snapshot when present", () => {
		const html = render({
			...BASE,
			judge: { runs: 48, history_runs: 30, cost_usd: history(0.12, 0.19, 0.31), basis_text: "30 past judge records" },
			notes: ["planner refused: arms A and B have the same config hash"],
			price_ref: "prices-2026-11-01",
		});
		expect(html).toContain('data-testid="estimate-judge"');
		expect(html).toContain("Judge, separate from the agent cost: 48 runs, $0.12 to $0.31 (median $0.19).");
		expect(html).toContain("30 past judge records");
		expect(html).toContain("planner refused: arms A and B have the same config hash");
		expect(html).toContain("Model prices come from prices-2026-11-01.");
	});

	it("renders the fixture with a budget-bound arm", () => {
		const fixture = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/estimate.json"), "utf8")) as Forecast;
		const html = render(fixture);
		expect(piece(html, "estimate-arm-cost-B")).toBe("at most $12.00");
		expect(html).toContain('data-basis="budget_bound"');
		expect(html).not.toContain("$0.00");
	});
});