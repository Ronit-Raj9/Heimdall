import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { VerdictTab } from "./verdictTab";

const asked = vi.hoisted(() => ({ verdictSkip: [] as boolean[] }));
const notFound = {
	data: undefined,
	isLoading: false,
	isError: true,
	error: { status: 404, data: { error: { message: "not found" } } },
	refetch: () => undefined,
};

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: () => [null, () => undefined],
}));
vi.mock("@/lib/store", async (importOriginal) => ({
	...((await importOriginal()) as Record<string, unknown>),
	useGetVerdictQuery: (_arg: unknown, opts?: { skip?: boolean }) => {
		asked.verdictSkip.push(Boolean(opts?.skip));
		return notFound;
	},
	useGetDiagnosticsQuery: () => notFound,
	useGetGridQuery: () => ({ data: undefined, isLoading: true, isError: false }),
	useGetCatalogQuery: () => ({ data: undefined, isLoading: true, isError: false }),
}));
vi.mock("@/lib/store/apis/migrationApi", () => ({ useGetCostExplainQuery: () => ({}) }));
// The grid asks whether the benchmark still exists (Q-76); here it does.
vi.mock("@/lib/store/apis/suitesLightApi", () => ({ useGetSuiteQuery: () => ({}) }));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...((await importOriginal()) as Record<string, unknown>),
	useGetExperimentQuery: (id: string) => ({
		data: { exp_id: id, state: "done", debug: id === "e_dbg" ? "replay" : undefined, debug_from_run: id === "e_dbg" ? "r_1" : undefined },
		isLoading: false,
		isError: false,
	}),
}));

// Fix wave 3, N247: GET /experiments/{id}/verdict answers 404 for a debug experiment (it is not analysed),
// so its Result tab says "Debug run (not scored)" and shows no error.
describe("VerdictTab for a debug experiment", () => {
	it("shows Debug run (not scored) and no error, and does not ask for the verdict", () => {
		asked.verdictSkip.length = 0;
		const html = renderToStaticMarkup(<VerdictTab expId="e_dbg" />);
		expect(html).toContain('data-testid="verdict-debug-not-scored"');
		expect(html).toContain("Debug run (not scored)");
		expect(html).not.toContain('data-testid="verdict-error"');
		expect(asked.verdictSkip).toEqual([true]);
	});
	it("still shows the load error for an ordinary comparison whose verdict is missing", () => {
		const html = renderToStaticMarkup(<VerdictTab expId="e_cmp" />);
		expect(html).toContain('data-testid="verdict-error"');
		expect(html).not.toContain("Debug run (not scored)");
	});
});