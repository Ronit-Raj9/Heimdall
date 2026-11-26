import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

const res = vi.hoisted(() => ({ value: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/traceToolsApi", () => ({ useSearchTracesQuery: () => res.value }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const { TraceMatches } = await import("./traceMatches");

const hit = {
	run_id: "r_1",
	task: "t1",
	outcome: "fail",
	benchmark: "b",
	matches: [{ field: "step", label: "model_response", text: "error 503", highlights: [] }],
};

describe("TraceMatches clear control", () => {
	it("renders Clear trace search with results, with none and on an error", () => {
		for (const value of [
			{ data: { hits: [hit], total: 1, indexed_runs: 1 }, isLoading: false, isFetching: false },
			{ data: { hits: [], total: 0, indexed_runs: 1 }, isLoading: false, isFetching: false },
			{ error: { status: 500, data: { error: { message: "x" } } }, isLoading: false, isFetching: false },
		]) {
			res.value = value;
			const html = render(<TraceMatches query="503" onClear={() => undefined} />);
			expect(html).toContain('data-testid="trace-search-clear"');
			expect(html).toContain("Clear trace search");
		}
	});
	it("separates the match label from its text", () => {
		res.value = { data: { hits: [hit], total: 1, indexed_runs: 1 }, isLoading: false, isFetching: false };
		expect(render(<TraceMatches query="503" onClear={() => undefined} />)).toContain("model_response: ");
	});
});