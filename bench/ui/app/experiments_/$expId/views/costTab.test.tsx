import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { CostTab } from "./costTab";

vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: () => [null, () => undefined],
}));
vi.mock("@/components/migration/migrationReport", () => ({ ExperimentParetoPanel: () => <div data-testid="pareto-stub" /> }));
vi.mock("@/components/experiments/gatewayOverheadPanel", () => ({
	GatewayOverheadPanel: () => <div data-testid="gateway-overhead-stub" />,
}));
vi.mock("@/components/migration/costWaterfall", () => ({ CostExplainPanel: () => <div data-testid="cost-explain-stub" /> }));

describe("CostTab (J7.cost)", () => {
	it("explains why there is no cost comparison on one setup instead of asking for one", () => {
		const html = render(<CostTab expId="e1" arms={["A"]} />);
		expect(html).toContain('data-testid="pareto-stub"');
		expect(html).not.toContain("cost-explain-stub");
		expect(html).toContain('data-testid="cost-explain-one-arm"');
		expect(html).toContain("Needs two setups. This run has one.");
	});
	it("shows the cost explanation with two setups", () => {
		const html = render(<CostTab expId="e1" arms={["A", "B"]} />);
		expect(html).toContain('data-testid="cost-explain-stub"');
		expect(html).not.toContain("cost-explain-one-arm");
	});
});