import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ClustersFilters, type ClustersFiltersProps } from "./clustersFilters";

const noop = () => undefined;
const props: ClustersFiltersProps = {
	suite: "coding-core",
	agent: "",
	exp: "e_1,e_2",
	since: "",
	min: 2,
	synthetic: "exclude",
	agentOptions: ["claude-code-sonnet", "codex-default"],
	experimentOptions: ["e_1", "e_2"],
	onText: noop,
	onMin: noop,
	onSynthetic: noop,
};

describe("ClustersFilters", () => {
	const html = renderToStaticMarkup(<ClustersFilters {...props} />);

	it("labels every control and the group", () => {
		for (const label of [">Benchmark<", ">Agent<", ">Comparisons<", ">Since<", ">Smallest group shown<", ">Runs<"]) expect(html).toContain(label);
		expect(html).toContain('aria-label="Filters"');
		// A labelled field: the label points at the input.
		expect(html).toMatch(/<label for="([^"]+)"[^>]*>Benchmark<\/label><input[^>]*id="\1"/);
	});

	it("shows the URL values in the fields", () => {
		expect(html).toContain('data-testid="clusters-filter-suite" value="coding-core"');
		expect(html).toContain('data-testid="clusters-filter-exp" value="e_1,e_2"');
		expect(html).toMatch(/type="number"[^>]*data-testid="clusters-filter-min" value="2"/);
	});

	it("presses the synthetic value, with all three choices", () => {
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="clusters-synthetic-exclude"/);
		expect(html).toMatch(/aria-pressed="false"[^>]*data-testid="clusters-synthetic-include"/);
		expect(html).toMatch(/aria-pressed="false"[^>]*data-testid="clusters-synthetic-only"/);
	});

	it("suggests agents and experiments from the list", () => {
		expect(html).toContain('<option value="codex-default">');
		expect(html).toContain('<option value="e_2">');
	});

	it("disables every control while loading", () => {
		const loading = renderToStaticMarkup(<ClustersFilters {...props} disabled />);
		expect(loading).toMatch(/disabled=""[^>]*data-testid="clusters-filter-agent"/);
		expect(loading).toMatch(/disabled=""[^>]*data-testid="clusters-synthetic-only"/);
	});
});
describe("benchmark select (M7-38)", () => {
	it("is a select of the given benchmarks, and keeps a URL value not in the list", () => {
		const html = renderToStaticMarkup(<ClustersFilters {...props} suiteOptions={["swe-mini@1", "web-tasks"]} />);
		expect(html).toMatch(/<select[^>]*data-testid="clusters-filter-suite"/);
		expect(html).toContain('<option value="swe-mini@1">swe-mini@1</option>');
		expect(html).toContain('<option value="coding-core" selected="">coding-core</option>');
	});
});
