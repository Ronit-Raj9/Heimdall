import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TrendsChart } from "./trendsChart";
import { trendsFixture } from "./trends.fixture";
import { seriesSlots } from "./trends.utils";

const data = trendsFixture();
const slots = seriesSlots(data.lineages, data.agents);
const render = (metric = data.metric, lineages = data.lineages) =>
	renderToStaticMarkup(<TrendsChart lineages={lineages} metric={metric} slots={slots} />);

describe("TrendsChart", () => {
	it("is an image with a title and a text summary of the newest values", () => {
		const html = render();
		expect(html).toMatch(/<svg role="img" aria-labelledby="[^"]+ [^"]+"/);
		expect(html).toContain("Verified success over time: 2 lineages, 5 points.");
		expect(html).toContain("codex (gpt-5.4-mini): 47.2% (Regressed)");
	});

	it("draws one line per lineage in its own slot color", () => {
		const html = render();
		expect(html).toMatch(/data-testid="trends-series-codex\|gpt-5.4-mini" data-slot="1"/);
		expect(html).toContain('stroke="var(--color-chart-cat-2)"');
		expect(html).toContain('stroke="var(--color-chart-cat-1)"');
	});

	it("marks a config change with a diamond and a flagged release with a ring", () => {
		const html = render();
		expect(html.match(/data-testid="trends-config-marker"/g)).toHaveLength(1);
		expect(html.match(/data-testid="trends-release-ring"/g)).toHaveLength(2);
		expect(html).toContain('data-config-changed="true"');
		expect(html).toContain('data-release="regressed"');
	});

	it("names the markers in the legend in words, not color alone", () => {
		const html = render();
		expect(html).toContain("Diamond: config hash changed from the previous point");
		expect(html).toContain("Red ring: release verdict regressed or blocked");
		expect(html).toContain("codex (gpt-5.4-mini)");
		expect(html).toContain("claude-code-gpt (openai/gpt-5.1)");
	});

	it("draws success intervals only for verified success", () => {
		expect(render()).toContain('data-testid="trends-interval"');
		const tokens = data.lineages.map((l) => ({ ...l, points: l.points.map((p) => ({ ...p, value: p.tokens })) }));
		const html = render("tokens", tokens);
		expect(html).not.toContain('data-testid="trends-interval"');
		expect(html).not.toContain("Bar: descriptive 95% interval");
	});

	it("leaves a point with no value off the line and says when nothing has a value", () => {
		const blank = data.lineages.map((l) => ({ ...l, points: l.points.map((p) => ({ ...p, value: null })) }));
		const html = render("cost_per_success", blank);
		expect(html).not.toContain('data-testid="trends-point"');
		expect(html).toContain("No point has a value for this metric.");
	});

	it("gives each point a hover title with its experiment and regression reason", () => {
		const html = render();
		expect(html).toContain("e_synthetic-providers");
		expect(html).toContain("Regressed: p 0.008 on 12 common tasks");
	});
});