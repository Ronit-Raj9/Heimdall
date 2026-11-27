import "@/lib/i18n";

import type { Estimate } from "@/lib/store/apis/evaluationsApi";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...((await importOriginal()) as Record<string, unknown>),
	useGetEvaluationReportQuery: () => ({ data: undefined, isLoading: false, isError: false, isFetching: false, refetch: () => undefined }),
}));

const { capNote, faultsSkippedAny, FailureTestEstimate, PastTest } = await import("./failureTestsView");

const est = (low: number, high: number) =>
	({ runs: 6, est_cost_usd: { low, high, known: true }, est_minutes: { low: 1, high: 4 } }) as unknown as Estimate;

describe("failure test cap note (Q-295)", () => {
	it("names the cap and the high estimate in the button's format when the estimate is above the cap", () => {
		expect(capNote(est(0.12, 15), 5)).toEqual({ cap: "$5.00", high: "$15" });
		expect(capNote(est(0.12, 4), 5)).toBeNull();
		expect(capNote(est(0.12, 15), null)).toBeNull();
	});

	it("shows the line under the estimate", () => {
		const html = render(
			<FailureTestEstimate problem={null} estimate={{ data: est(0.12, 15) }} pending={false} budgetTyped={false} capUsd={5} />,
		);
		expect(html).toContain("Stops at the $5.00 budget. The full run may need up to $15.");
	});
});

describe("failure test skipped faults (Q-292)", () => {
	it("collects a fault skipped by any picked benchmark", () => {
		const e = {
			per_benchmark: [
				{ benchmark: "hd-harbor-tb2", faults_skipped: [{ fault: "broken-tool", reason: "no probe" }] },
				{ benchmark: "x", faults_skipped: null },
			],
		} as unknown as Estimate;
		expect([...faultsSkippedAny(e)]).toEqual(["broken-tool"]);
	});
});

describe("one fault name in form and results (F10-15)", () => {
	it("past results use the catalog label the form shows", () => {
		const ev = {
			id: "ev_1",
			status: "running",
			created_at: "2026-11-22T10:00:00Z",
			spec: { name: "x", arms: [{ label: "A", agent: "codex", model: "m" }], faults: ["clean", "model-down"] },
			children: [],
		};
		const html = render(<PastTest ev={ev as never} catalog={[{ id: "model-down", label: "Model down" }]} />);
		expect(html).toContain("Model down");
	});
});

describe("monitors need a finished comparison (Q-308)", () => {
	it("counts only done comparisons", async () => {
		const { hasFinishedComparison } = await import("@/app/monitors/views/monitorsView");
		expect(hasFinishedComparison([{ state: "running" }])).toBe(false);
		expect(hasFinishedComparison([{ state: "done" }])).toBe(true);
	});
});
