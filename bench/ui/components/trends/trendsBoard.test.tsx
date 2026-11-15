import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TrendsBoard } from "./trendsBoard";
import { trendsFixture, truncated } from "./trends.fixture";

const data = trendsFixture();

describe("TrendsBoard", () => {
	it("shows the lineage flags, the chart and the table", () => {
		const html = renderToStaticMarkup(<TrendsBoard data={data} />);
		expect(html).toContain('data-testid="trends-lineages"');
		expect(html).toContain('data-testid="trends-chart"');
		expect(html).toContain('data-testid="trends-table"');
		expect(html).toMatch(/data-testid="trends-lineage-codex\|gpt-5.4-mini"/);
		expect(html).toContain("Verified success over time");
	});

	it("says where the numbers come from and how many runs were rejected", () => {
		const html = renderToStaticMarkup(<TrendsBoard data={data} />);
		expect(html).toContain("never this index");
		expect(html).toContain("1 run is listed as rejected because its seal did not verify.");
	});

	it("shows the empty state below two points, with the point count", () => {
		for (const n of [0, 1]) {
			const html = renderToStaticMarkup(<TrendsBoard data={truncated(data, n)} />);
			expect(html).toContain('data-testid="trends-empty"');
			expect(html).not.toContain('data-testid="trends-chart"');
		}
		expect(renderToStaticMarkup(<TrendsBoard data={truncated(data, 1)} />)).toContain("Only 1 point matches these filters.");
		expect(renderToStaticMarkup(<TrendsBoard data={truncated(data, 2)} />)).toContain('data-testid="trends-chart"');
	});

	it("offers synthetic runs from an empty live-only response", () => {
		const live = { ...truncated(data, 0), synthetic: "exclude" as const };
		expect(renderToStaticMarkup(<TrendsBoard data={live} onIncludeSynthetic={() => undefined} />)).toContain(
			'data-testid="trends-include-synthetic"',
		);
		expect(renderToStaticMarkup(<TrendsBoard data={truncated(data, 0)} onIncludeSynthetic={() => undefined} />)).not.toContain(
			'data-testid="trends-include-synthetic"',
		);
	});
});