import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { MigrationReport } from "@/lib/types/bench";
import { MIGRATION_FIXTURE } from "./migration.fixtures";
import { MigrationReportView } from "./migrationReport";

// A plain anchor stands in for the router's Link, so the target URL is visible in the markup.
vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search: { pair: string }; children: React.ReactNode }) => (
		<a href={`${to}?pair=${search.pair}`} {...rest}>
			{children}
		</a>
	),
}));

const render = (r: MigrationReport) => renderToStaticMarkup(<MigrationReportView report={r} />);

describe("MigrationReportView", () => {
	const html = render(MIGRATION_FIXTURE);

	it("leads with the recommendation, never stronger than the tests", () => {
		expect(html).toMatch(/data-testid="migration-recommendation-B" data-action="not_enough_evidence"/);
		expect(html).toContain("Not enough evidence.");
		expect(html).toContain("Release policy: enterprise-default.");
	});

	it("lists every stratum with its Holm family and the tasks needed", () => {
		expect(html).toContain("Every stratum of this pair is one Holm family of");
		expect(html).toContain("Fault: empty-test-run");
		expect(html).toContain("Task kind: ");
		expect(html).toMatch(/data-result="cannot_tell"/);
	});

	it("links a flipped task to its run pair and shows the cost and the Pareto plane", () => {
		expect(html).toMatch(/href="\/compare\?pair=[^"]+"[^>]*data-testid="migration-flipped-link"/);
		expect(html).toContain('data-testid="migration-pareto"');
		expect(html).toContain('data-testid="migration-cost"');
	});

	it("keeps the tuned-prompt and harness notes", () => {
		expect(html).toContain("not a tuned prompt");
		expect(html).toContain("different agent configs");
	});

	it("marks a synthetic report and a do-not-migrate pair", () => {
		const pair = {
			...MIGRATION_FIXTURE.pairs[0],
			recommendation: { ...MIGRATION_FIXTURE.pairs[0].recommendation, action: "do_not_migrate" as const },
		};
		const out = render({ ...MIGRATION_FIXTURE, synthetic: true, pairs: [pair] });
		expect(out).toContain('data-testid="migration-synthetic"');
		expect(out).toContain("Do not migrate.");
	});
});