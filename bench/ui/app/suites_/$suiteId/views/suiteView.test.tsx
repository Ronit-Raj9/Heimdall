import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import SuiteView from "./suiteView";

// QC2-27-09: a task link from frozen evidence names its suite version; when the catalog holds a
// newer revision the page says it shows the current one.

const broken = { id: "example", version: "2.0.0", description: "", tasks: [], lock_state: "fresh", error: "broken for the test" };
const fixture = vi.hoisted(() => ({ suites: [] as unknown[], runs: undefined as unknown, health: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetSuitesQuery: () => okQuery({ mode: "live", suites: fixture.suites }),
	useGetSuiteHealthQuery: () => fixture.health,
	useListRunsQuery: () => fixture.runs ?? okQuery({ runs: [], total: 0, limit: 1 }),
}));
vi.mock("./suiteTabs", () => ({ SuiteExportTab: () => null, SuiteHealthTab: () => null, SuiteTiersTab: () => null }));
vi.mock("./calibrationTab", () => ({ SuiteCalibrationTab: () => null }));
vi.mock("./tasksTab", () => ({ SuiteTasksTab: () => null }));
vi.mock("@/components/suites/suiteFacts", () => ({ BrokenSuite: () => null, SuiteDriftNotice: () => null, SuiteFacts: () => null }));
vi.mock("@/components/suites/suiteRunButton", () => ({ SuiteRunButton: () => null }));
vi.mock("@/components/suites/suiteLockKind", () => ({ FingerprintDriftNotice: () => null }));
vi.mock("@/components/reference/referenceBoard", () => ({ ReferenceBoardPanel: () => null }));
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: () => [null, () => undefined],
}));

describe("SuiteView frozen version", () => {
	it("flags a link to an older version than the current suite", () => {
		fixture.suites = [broken];
		const html = render(<SuiteView suiteId="example@1.0.0" />);
		expect(html).toContain('data-testid="suite-version-mismatch"');
		expect(html).toContain("1.0.0");
	});
	it("stays quiet for the current version or an unversioned link", () => {
		fixture.suites = [broken];
		expect(render(<SuiteView suiteId="example@2.0.0" />)).not.toContain("suite-version-mismatch");
		expect(render(<SuiteView suiteId="example" />)).not.toContain("suite-version-mismatch");
	});
});

const healthy = (extra: Record<string, unknown>) => ({
	id: "hd-swe-verified",
	version: "1.0.0",
	description: "",
	tasks: [],
	task_count: 0,
	admitted_count: 0,
	probes: [],
	drift: [],
	lock_state: "locked",
	...extra,
});

describe("SuiteView difficulty levels tab (C2.levels)", () => {
	it("is hidden when only the implicit nightly level exists", () => {
		fixture.suites = [healthy({ tiers: [{ name: "nightly", tasks: ["a", "b"], defined: false }] })];
		const html = render(<SuiteView suiteId="hd-swe-verified" />);
		expect(html).not.toContain("Difficulty levels");
	});
	it("counts defined levels only", () => {
		fixture.suites = [
			healthy({
				tiers: [
					{ name: "nightly", tasks: ["a", "b"], defined: false },
					{ name: "smoke", tasks: ["a"], defined: true },
				],
			}),
		];
		const html = render(<SuiteView suiteId="hd-swe-verified" />);
		expect(html).toMatch(/Difficulty levels[\s\S]{0,400}?>1</);
		expect(html).not.toMatch(/Difficulty levels[\s\S]{0,400}?>2</);
	});
});

describe("SuiteView built-in note (C2.tabs)", () => {
	it("tells how to get an editable benchmark, with a link to Add a benchmark", () => {
		fixture.suites = [healthy({ builtin: true, source: "library" })];
		const html = render(<SuiteView suiteId="hd-swe-verified" />);
		expect(html).toContain("To build, edit or review tasks, make your own benchmark.");
		expect(html).toMatch(/href="\/suites\/add"[^>]*>Add a benchmark</);
	});
	it("shows a team benchmark by its id as typed", () => {
		fixture.suites = [healthy({ id: "zz-test-own" })];
		const html = render(<SuiteView suiteId="zz-test-own" />);
		expect(html).not.toContain("Zz Test Own");
		expect(html).not.toContain("make your own benchmark");
	});
});
describe("SuiteView stat tiles (N164)", () => {
	const bfcl = () => healthy({ id: "hd-harbor-bfcl", builtin: true, source: "library" });
	it("counts runs from the run list, not the health report", () => {
		fixture.suites = [bfcl()];
		fixture.health = { isError: true, error: { status: 500 } };
		fixture.runs = okQuery({ runs: [], total: 59, limit: 1 });
		const html = render(<SuiteView suiteId="hd-harbor-bfcl" />);
		expect(html).toMatch(/Runs so far[\s\S]{0,400}?>59</);
		expect(html).not.toContain("None yet");
	});
	it("says Counting while the run list loads and - with the error when it fails", () => {
		fixture.suites = [bfcl()];
		fixture.health = {};
		fixture.runs = { data: undefined, isLoading: true, isFetching: true, isError: false };
		expect(render(<SuiteView suiteId="hd-harbor-bfcl" />)).toContain("Counting...");
		fixture.runs = {
			data: undefined,
			isLoading: false,
			isError: true,
			error: { status: 500, data: { error: { message: "run store down" } } },
		};
		const html = render(<SuiteView suiteId="hd-harbor-bfcl" />);
		expect(html).toMatch(/title="[^"]*"[^>]*>-</);
		expect(html).not.toContain("None yet");
	});
	it("says None yet only when the benchmark has no runs", () => {
		fixture.suites = [bfcl()];
		fixture.runs = okQuery({ runs: [], total: 0, limit: 1 });
		expect(render(<SuiteView suiteId="hd-harbor-bfcl" />)).toContain("None yet");
	});
	it("labels the benchmark's own failure tests and says the model faults work on every benchmark", () => {
		fixture.suites = [bfcl()];
		fixture.runs = undefined;
		const html = render(<SuiteView suiteId="hd-harbor-bfcl" />);
		expect(html).toContain("Its own failure tests");
		expect(html).toContain("Plus model faults (outage, brownout, flaky) that work on every benchmark.");
	});
	it("says it is working out task health while the health report loads", () => {
		fixture.suites = [bfcl()];
		fixture.health = { isLoading: true };
		expect(render(<SuiteView suiteId="hd-harbor-bfcl" />)).toContain("Working out task health...");
		fixture.health = {};
	});
});