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
		expect(html).not.toContain("migration-report\"");
		expect(html).not.toContain("migration-effective");
	});
});
