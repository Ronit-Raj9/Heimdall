import "@/lib/i18n";

import { render, tagOf } from "@/lib/testing/render";
import type { RegradeRef } from "@/lib/types/bench";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ORIGINAL_GRADE, RegradeNotice, effectiveGrade, gradeParam, useGradeChoice } from "./gradeChoice";
import { VerdictTab } from "./verdictTab";

// After a re-score, the comparison page shows the newest one by default with one line naming it and a
// link to the original, and the reverse link when the original is asked for (?grade=original).

const page = vi.hoisted(() => ({
	query: {} as Record<string, string | null>,
	latest: undefined as RegradeRef | undefined,
	verdictArgs: [] as unknown[],
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: (key: string) => [page.query[key] ?? null, () => undefined],
}));
const notFound = { data: undefined, isLoading: false, isError: true, error: { status: 404 }, refetch: () => undefined };
vi.mock("@/lib/store", async (importOriginal) => ({
	...((await importOriginal()) as Record<string, unknown>),
	useGetVerdictQuery: (arg: unknown) => {
		page.verdictArgs.push(arg);
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
		data: { exp_id: id, state: "done", latest_regrade: page.latest },
		isLoading: false,
		isError: false,
	}),
}));

const latest: RegradeRef = { id: "rg_new", created_at: "2026-11-23T09:30:00Z", note: "Model check 1.1.0." };

function Probe({ expId }: { expId: string }) {
	const { grade } = useGradeChoice(expId);
	return <span data-testid="probe" data-grade={grade ?? "original"} />;
}

beforeEach(() => {
	page.query = { tab: "verdict" };
	page.latest = latest;
	page.verdictArgs = [];
});

describe("effectiveGrade and gradeParam", () => {
	it("shows the newest re-score unless the URL names a grade source or the original", () => {
		expect(effectiveGrade(null, latest)).toBe("rg_new");
		expect(effectiveGrade(ORIGINAL_GRADE, latest)).toBeNull();
		expect(effectiveGrade("rg_old", latest)).toBe("rg_old");
		expect(effectiveGrade("reviewed", latest)).toBe("reviewed");
		expect(effectiveGrade(null, undefined)).toBeNull();
	});
	it("asks for the original explicitly only when a re-score would otherwise show", () => {
		expect(gradeParam(null, latest)).toBe(ORIGINAL_GRADE);
		expect(gradeParam(ORIGINAL_GRADE, undefined)).toBeNull();
		expect(gradeParam(null, undefined)).toBeNull();
		expect(gradeParam("reviewed", latest)).toBe("reviewed");
	});
});

describe("the page's default grade source", () => {
	it("is the newest re-score from the status", () => {
		expect(tagOf(render(<Probe expId="e_x" />), "probe")).toContain('data-grade="rg_new"');
		render(<VerdictTab expId="e_x" />);
		expect(page.verdictArgs).toContainEqual({ expId: "e_x", gradeSource: "rg_new" });
	});
	it("is the original with ?grade=original, or without any re-score", () => {
		page.query.grade = ORIGINAL_GRADE;
		expect(tagOf(render(<Probe expId="e_x" />), "probe")).toContain('data-grade="original"');
		render(<VerdictTab expId="e_x" />);
		// The server's default is the newest re-score (wave 4, N200), so the original is asked for by name.
		expect(page.verdictArgs.at(-1)).toEqual({ expId: "e_x", gradeSource: ORIGINAL_GRADE });
		page.query.grade = null;
		page.latest = undefined;
		expect(tagOf(render(<Probe expId="e_x" />), "probe")).toContain('data-grade="original"');
	});
});

describe("RegradeNotice", () => {
	it("names the re-score shown by default, its date and note, and links the original", () => {
		const html = render(<RegradeNotice expId="e_x" />);
		expect(tagOf(html, "regrade-notice")).toContain('data-showing="regrade"');
		expect(html).toMatch(/Showing re-score rg_new from [^<]*2026[^<]*: Model check 1\.1\.0\./);
		expect(html).not.toContain("1.1.0..");
		const link = tagOf(html, "regrade-notice-original");
		expect(link).toContain('href="/experiments/e_x?tab=verdict&amp;grade=original"');
		expect(html).toContain("Show original result");
		expect(html).not.toMatch(/[–—]/);
	});
	it("leaves the note out when the re-score has none", () => {
		page.latest = { id: "rg_new", created_at: "2026-11-23T09:30:00Z" };
		const html = render(<RegradeNotice expId="e_x" />);
		expect(html).toMatch(/Showing re-score rg_new from [^<]*2026[^<]*\.</);
	});
	it("says the original is shown and links the newer re-score", () => {
		page.query.grade = ORIGINAL_GRADE;
		const html = render(<RegradeNotice expId="e_x" />);
		expect(tagOf(html, "regrade-notice")).toContain('data-showing="original"');
		expect(html).toContain("Original result. Newer re-score rg_new from");
		expect(tagOf(html, "regrade-notice-latest")).toContain('href="/experiments/e_x?tab=verdict"');
		expect(html).toContain("Show re-score");
	});
	it("is absent without a re-score, and when another grade source is picked", () => {
		page.latest = undefined;
		expect(render(<RegradeNotice expId="e_x" />)).toBe("");
		page.latest = latest;
		page.query.grade = "rg_old";
		expect(render(<RegradeNotice expId="e_x" />)).toBe("");
	});
});