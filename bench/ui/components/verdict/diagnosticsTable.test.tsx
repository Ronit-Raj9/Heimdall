import "@/lib/i18n";

import type { MetricDescriptor } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { DiagnosticsTable } from "./diagnosticsTable";
import { diagnosticsFixture } from "./testFixtures";

const metric = (name: string, label: string, kind: MetricDescriptor["kind"]): MetricDescriptor => ({
	name,
	version: "1.0.0",
	label,
	kind,
	higher_is_better: false,
	min_evidence: "gateway",
	description: `${label} description`,
	ps2: true,
});

const METRICS = {
	steps: metric("steps", "Steps (model turns)", "count"),
	tokens: metric("tokens", "Tokens", "tokens"),
	cost_usd: metric("cost_usd", "Cost (USD)", "usd"),
	wall_time_s: metric("wall_time_s", "Wall time", "duration"),
};

function render(): string {
	return renderToStaticMarkup(<DiagnosticsTable diagnostics={diagnosticsFixture} arms={["A", "B"]} metrics={METRICS} />);
}

describe("DiagnosticsTable", () => {
	it("renders one row per condition with per-arm ratios", () => {
		const html = render();
		expect(html).toMatch(/data-testid="verdict-diagnostics-row-clean" data-descriptive="true"/);
		expect(html).toMatch(/data-testid="verdict-diagnostics-row-stale-notes" data-descriptive="false"/);
		expect(html).toContain(">9/12<");
		expect(html).toContain(">10/12<");
		expect(html).toContain(">4 tasks<");
	});

	it("labels descriptive rows and marks the primary one", () => {
		const html = render();
		const clean = html.slice(html.indexOf("verdict-diagnostics-effect-clean"));
		expect(clean.slice(0, 400)).toContain(">descriptive<");
		const primary = html.slice(html.indexOf("verdict-diagnostics-effect-stale-notes"));
		expect(primary.slice(0, 400)).toContain(">+41.7 pp<");
		expect(primary.slice(0, 400)).toContain(">primary<");
	});

	it("renders the meters through ValueCell by kind, unknown hatched", () => {
		const html = render();
		expect(html).toContain(">Honesty<");
		expect(html).toContain(">Safety<");
		expect(html).toContain(">Efficiency<");
		expect(html).toContain(">27.8%<");
		expect(html).toContain(">$0.41<");
		expect(html).toMatch(/data-state="unknown" data-kind="usd" data-testid="verdict-meter-meter-cost_per_verified_success-B"/);
		expect(html).toContain("Steps (model turns) (median per run)");
		expect(html).toContain(">182.4K<");
		expect(html).toContain(">5.2m<");
		expect(html).toContain('data-testid="verdict-method-costPerSuccess"');
		expect(html).toContain("success claims on failed runs over runs with a success claim.");
	});

	it("never renders an unknown meter value as zero", () => {
		const html = render();
		const cell = html.slice(html.indexOf('data-testid="verdict-meter-meter-cost_per_verified_success-B"'));
		expect(cell.slice(0, 200)).not.toMatch(/>\$0(\.00)?</);
	});

	it("says so when there are no rows or meters", () => {
		const html = renderToStaticMarkup(
			<DiagnosticsTable diagnostics={{ diagnostics: [], meters: [], arm_metrics: {} }} arms={["A", "B"]} />,
		);
		expect(html).toContain('data-testid="verdict-diagnostics-empty"');
		expect(html).toContain('data-testid="verdict-meters-empty"');
	});
});