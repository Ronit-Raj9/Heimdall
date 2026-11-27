import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import SuiteView, { aliasSection, headerFacts, TAB_ALIASES } from "./suiteView";

// QC2-27-09: a task link from frozen evidence names its suite version; when the catalog holds a
// newer revision the page says it shows the current one.

const broken = { id: "example", version: "2.0.0", description: "", tasks: [], lock_state: "fresh", error: "broken for the test" };
const fixture = vi.hoisted(() => ({ suites: [] as unknown[], runs: undefined as unknown, health: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetSuiteHealthQuery: () => fixture.health,
	useListRunsQuery: () => fixture.runs ?? okQuery({ runs: [], total: 0, limit: 1 }),
}));
// N194: the page reads one benchmark from GET /suites/{id}; an id the fixture does not hold is a 404.
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuiteQuery: (id: string) => {
		const s = (fixture.suites as { id: string }[]).find((x) => x.id === id);
		if (s) return { ...okQuery({ mode: "live", ...s }), currentData: { mode: "live", ...s } };
		return {
			data: undefined,
			currentData: undefined,
			error: { status: 404 },
			isLoading: false,
			isFetching: false,
			isError: true,
			isSuccess: false,
			refetch: () => undefined,
		};
	},
}));
vi.mock("./suiteTabs", () => ({ SuiteExportTab: () => null, SuiteHealthTab: () => null, SuiteTiersTab: () => null }));
vi.mock("./calibrationTab", () => ({ SuiteCalibrationTab: () => null }));
vi.mock("./tasksTab", () => ({ SuiteTasksTab: () => null }));
vi.mock("@/components/suites/suiteFacts", () => ({ BrokenSuite: () => null, SuiteDriftNotice: () => null, SuiteFacts: () => null }));
vi.mock("@/components/suites/suiteRunButton", () => ({ SuiteRunButton: () => null }));
vi.mock("@/components/suites/suiteLockKind", () => ({ FingerprintDriftNotice: () => null }));
vi.mock("@/components/reference/referenceBoard", () => ({ ReferenceBoardPanel: () => null }));
vi.mock("@/lib/store/apis/referenceApi", () => ({ useGetReferenceBoardQuery: () => ({ data: undefined }) }));
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
		// M6-11: difficulty levels are a section of the Quality tab; ?tab=tiers still opens it there.
		const html = render(<SuiteView suiteId="hd-swe-verified" />);
		expect(html).not.toContain("Difficulty levels");
		expect(html).toContain("Quality");
		expect(TAB_ALIASES.quality).toContain("tiers");
		expect(aliasSection("tiers")).toBe("suite-section-tiers");
		expect(aliasSection("ops")).toBe("suite-section-readiness");
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
		expect(html).toContain("59 runs so far");
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
		expect(render(<SuiteView suiteId="hd-harbor-bfcl" />)).toContain("no runs yet");
	});
	it("labels the benchmark's own failure tests and says the model faults work on every benchmark", () => {
		fixture.suites = [{ ...bfcl(), probes: [{ id: "a" }, { id: "b" }] }];
		fixture.runs = undefined;
		const html = render(<SuiteView suiteId="hd-harbor-bfcl" />);
		// D1 N164 (lead's rule 17): the header names them built-in failure tests. M6-10: in the one facts line,
		// which has no room for the tile's model faults note (the Quality tab lists the failure tests).
		expect(html).toContain("2 built-in failure tests");
	});
	it("says it is working out task health while the health report loads", () => {
		fixture.suites = [bfcl()];
		fixture.health = { isLoading: true };
		expect(render(<SuiteView suiteId="hd-harbor-bfcl" />)).toContain("Working out task health...");
		fixture.health = {};
	});
});

describe("SuiteView facts line (M6-10)", () => {
	it("joins the parts with commas and skips empty ones", () => {
		expect(headerFacts({ tasks: "3641 tasks", ready: "36 ready", probes: null, runs: "103 runs so far" })).toBe(
			"3641 tasks, 36 ready, 103 runs so far",
		);
	});
});
