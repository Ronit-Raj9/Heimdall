import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import SuiteView from "./suiteView";

// QC2-27-09: a task link from frozen evidence names its suite version; when the catalog holds a
// newer revision the page says it shows the current one.

const suite = { id: "example", version: "2.0.0", description: "", tasks: [], lock_state: "fresh", error: "broken for the test" };
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetSuitesQuery: () => okQuery({ mode: "live", suites: [suite] }),
	useGetSuiteHealthQuery: () => ({}),
}));
vi.mock("./suiteTabs", () => ({ SuiteExportTab: () => null, SuiteHealthTab: () => null, SuiteTiersTab: () => null }));
vi.mock("./calibrationTab", () => ({ SuiteCalibrationTab: () => null }));
vi.mock("./tasksTab", () => ({ SuiteTasksTab: () => null }));
vi.mock("@/components/suites/suiteFacts", () => ({ BrokenSuite: () => null, SuiteDriftNotice: () => null, SuiteFacts: () => null }));

describe("SuiteView frozen version", () => {
	it("flags a link to an older version than the current suite", () => {
		const html = render(<SuiteView suiteId="example@1.0.0" />);
		expect(html).toContain('data-testid="suite-version-mismatch"');
		expect(html).toContain("1.0.0");
	});
	it("stays quiet for the current version or an unversioned link", () => {
		expect(render(<SuiteView suiteId="example@2.0.0" />)).not.toContain("suite-version-mismatch");
		expect(render(<SuiteView suiteId="example" />)).not.toContain("suite-version-mismatch");
	});
});
