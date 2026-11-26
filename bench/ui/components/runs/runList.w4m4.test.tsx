import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

const search = vi.hoisted(() => ({ value: {} as Record<string, unknown> }));
const list = vi.hoisted(() => ({ value: { runs: [] as unknown[], total: 0 } }));
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryStates: () => [search.value, () => undefined],
}));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useListRunsQuery: () => okQuery({ ...list.value, limit: 50, offset: 0, facets: {} }),
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const { RunList } = await import("./runList");

describe("RunList", () => {
	it("says the comparison is still running instead of offering a new one", () => {
		search.value = {};
		list.value = { runs: [], total: 0 };
		const html = render(<RunList expId="e_1" running />);
		expect(html).toContain("Runs show up here as they finish. This comparison is still running.");
		expect(html).not.toContain("/experiments/new");
	});
	it("past the last page says so, offers the last page and keeps the pager", () => {
		search.value = { page: 99 };
		list.value = { runs: [], total: 324 };
		const html = render(<RunList expId="e_1" />);
		expect(html).toContain("This page is past the end of the list.");
		expect(html).toContain("Go to the last page");
		expect(html).not.toContain("run-list-empty");
	});
});