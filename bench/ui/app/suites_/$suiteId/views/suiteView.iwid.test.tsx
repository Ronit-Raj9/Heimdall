import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

// N194: the benchmark page asks GET /suites/{id} for its one benchmark and never loads the whole list.
const calls = vi.hoisted(() => ({ one: [] as string[], list: 0, status: 200 as number }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetSuitesQuery: () => {
		calls.list += 1;
		return okQuery({ mode: "live", suites: [] });
	},
	useGetSuiteHealthQuery: () => ({}),
	useListRunsQuery: () => okQuery({ runs: [], total: 0, limit: 1 }),
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuiteQuery: (id: string) => {
		calls.one.push(id);
		if (calls.status === 404) {
			return {
				data: undefined,
				currentData: undefined,
				error: { status: 404 },
				isLoading: false,
				isFetching: false,
				isError: true,
				refetch: () => undefined,
			};
		}
		if (calls.status === 500) {
			return {
				data: undefined,
				currentData: undefined,
				error: { status: 500 },
				isLoading: false,
				isFetching: false,
				isError: true,
				refetch: () => undefined,
			};
		}
		const s = {
			mode: "live",
			id,
			version: "1.0.0",
			description: "",
			tasks: [],
			task_count: 0,
			admitted_count: 0,
			probes: [],
			drift: [],
			lock_state: "locked",
		};
		return { ...okQuery(s), currentData: s };
	},
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

const { default: SuiteView } = await import("./suiteView");

describe("SuiteView reads one benchmark (N194)", () => {
	it("asks GET /suites/{id} with the id without its version and never the full list", () => {
		calls.one = [];
		calls.list = 0;
		calls.status = 200;
		const html = render(<SuiteView suiteId="faq-bench@1.0.0" />);
		expect(calls.one).toContain("faq-bench");
		expect(calls.list).toBe(0);
		expect(html).not.toContain('data-testid="suite-not-found"');
		expect(html).not.toContain('data-testid="suite-error"');
	});
	it("shows not found for a 404 and the load error for any other failure", () => {
		calls.status = 404;
		expect(render(<SuiteView suiteId="nope" />)).toContain('data-testid="suite-not-found"');
		calls.status = 500;
		const html = render(<SuiteView suiteId="nope" />);
		expect(html).toContain('data-testid="suite-error"');
		expect(html).not.toContain('data-testid="suite-not-found"');
	});
});