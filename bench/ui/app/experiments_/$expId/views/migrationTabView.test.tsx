import "@/lib/i18n";

import { MIGRATION_FIXTURE } from "@/components/migration/migration.fixtures";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { MigrationTab } from "./migrationTab";

// QC2-27-07: the badges show the settings the rendered report was built with, and a changed bookmark
// never shows the previous report beside the new controls.

const api = vi.hoisted(() => ({ query: {} as unknown, search: {} as Record<string, string | null> }));
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: (key: string) => [api.search[key] ?? null, () => undefined],
}));
vi.mock("@/lib/store/apis/migrationApi", () => ({ useGetMigrationQuery: () => api.query }));
vi.mock("@/components/migration/migrationReport", () => ({ MigrationReportView: () => <div data-testid="migration-report" /> }));
const ms = vi.hoisted(() => ({ config: {} as unknown }));
vi.mock("@/lib/store/apis/modelSwitchApi", () => ({ useGetModelSwitchConfigQuery: () => ms.config }));
vi.mock("@/components/modelswitch/applyPanel", () => ({
	ApplyPanel: ({ evaluationId, ruleCount }: { evaluationId: string; ruleCount: number }) => (
		<div data-testid="apply-panel-stub">
			{evaluationId}:{ruleCount}
		</div>
	),
}));

describe("MigrationTab effective settings", () => {
	it("shows the response's alpha and grade source, not the address bar's", () => {
		api.search = { alpha: "0.2", grade_source: "reviewed" };
		const report = { ...MIGRATION_FIXTURE, alpha: 0.05, grade_source: "original" };
		api.query = { data: report, currentData: report, isLoading: false };
		const html = render(<MigrationTab expId="e1" arms={["A", "B"]} />);
		expect(html).toContain("α = 0.05");
		expect(html).not.toContain("α = 0.2");
		expect(html).toContain('data-testid="migration-report"');
	});
	it("waits for the current selection's report instead of showing the previous one", () => {
		api.search = { grade_source: "reviewed" };
		api.query = { data: MIGRATION_FIXTURE, currentData: undefined, isLoading: false, isFetching: true };
		const html = render(<MigrationTab expId="e1" arms={["A", "B"]} />);
		expect(html).toContain('data-testid="migration-report-loading"');
		expect(html).not.toContain('migration-report"');
		expect(html).not.toContain("migration-effective");
	});
});

describe("MigrationTab Apply to my Bifrost (D9.apply)", () => {
	const report = { ...MIGRATION_FIXTURE, alpha: 0.05, grade_source: "original" };
	const config = { ready: true, config: { governance: { routing_rules: [{}, {}, {}] } } };
	it("renders the apply panel for a comparison an evaluation made", () => {
		api.search = {};
		api.query = { data: report, currentData: report, isLoading: false };
		ms.config = { data: config, isLoading: false };
		const html = render(<MigrationTab expId="e1" arms={["A", "B"]} evaluationId="ev_1" />);
		expect(html).toContain('data-testid="apply-panel-stub"');
		expect(html).toContain("ev_1:3");
	});
	it("says why when the evaluation is not a model comparison", () => {
		api.query = { data: report, currentData: report, isLoading: false };
		ms.config = { error: { status: 400, data: { error: { message: "not a model comparison" } } }, isLoading: false };
		const html = render(<MigrationTab expId="e1" arms={["A", "B"]} evaluationId="ev_1" />);
		expect(html).not.toContain("apply-panel-stub");
		expect(html).toContain('data-testid="migration-apply-unavailable"');
		expect(html).toContain("not a model comparison");
	});
	it("has no apply panel outside an evaluation", () => {
		api.query = { data: report, currentData: report, isLoading: false };
		ms.config = { data: config, isLoading: false };
		const html = render(<MigrationTab expId="e1" arms={["A", "B"]} />);
		expect(html).not.toContain("apply-panel-stub");
	});
});