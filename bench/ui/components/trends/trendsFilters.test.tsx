import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TrendsFilters, fromPicker, optionsWithCurrent, type TrendsFiltersProps } from "./trendsFilters";
import { ALL_VALUE, TREND_METRICS } from "./trends.utils";

const noop = () => undefined;
const props: TrendsFiltersProps = {
	suite: "",
	suites: ["coding-core@1.0.0", "coding-core@0.9.0"],
	agent: "codex",
	agents: ["claude-code-gpt", "codex"],
	metric: "verified_success",
	metrics: [...TREND_METRICS],
	condition: "clean",
	conditions: ["clean", "model-outage"],
	synthetic: "exclude",
	onSuite: noop,
	onAgent: noop,
	onMetric: noop,
	onCondition: noop,
	onSynthetic: noop,
};

describe("TrendsFilters", () => {
	it("renders one labelled row of every filter", () => {
		const html = renderToStaticMarkup(<TrendsFilters {...props} />);
		expect(html).toContain('aria-label="Trend filters"');
		for (const id of [
			"trends-suite-picker",
			"trends-agent-picker",
			"trends-metric-picker",
			"trends-condition-picker",
			"trends-synthetic",
		]) {
			expect(html).toContain(`data-testid="${id}"`);
		}
		for (const label of [">Benchmark<", ">Agent<", ">Measurement<", ">Conditions<", ">Runs<"]) expect(html).toContain(label);
		// Each picker's label points at its trigger.
		expect(html).toMatch(/<label for="([^"]+)"[^>]*>Measurement<\/label><button[^>]*id="\1"/);
	});

	it("maps the all option to no filter and keeps an unlisted current value", () => {
		expect(fromPicker(ALL_VALUE)).toBe("");
		expect(fromPicker("codex")).toBe("codex");
		expect(optionsWithCurrent("old@0.1.0", ["coding-core@1.0.0"])).toEqual(["old@0.1.0", "coding-core@1.0.0"]);
		expect(optionsWithCurrent("coding-core@1.0.0", ["coding-core@1.0.0"])).toEqual(["coding-core@1.0.0"]);
		expect(optionsWithCurrent("", ["a"])).toEqual(["a"]);
	});

	it("presses the synthetic value and explains each option", () => {
		const html = renderToStaticMarkup(<TrendsFilters {...props} synthetic="include" />);
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="trends-synthetic-include"/);
		expect(html).toMatch(/aria-pressed="false"[^>]*data-testid="trends-synthetic-exclude"/);
		expect(html).toMatch(/aria-pressed="false"[^>]*data-testid="trends-synthetic-only"/);
		expect(html).toContain('title="Scripted agent or scripted model runs only. Their numbers are not findings."');
	});

	it("disables the condition picker when there is nothing to pick", () => {
		const html = renderToStaticMarkup(<TrendsFilters {...props} condition="" conditions={[]} />);
		expect(html).toMatch(/data-testid="trends-condition-picker"[^>]*disabled|disabled[^>]*data-testid="trends-condition-picker"/);
	});
});