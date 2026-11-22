import "@/lib/i18n";

import type { Evaluation, Report } from "@/lib/store/apis/evaluationsApi";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const fx = vi.hoisted(() => ({ report: undefined as unknown }));

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...((await importOriginal()) as Record<string, unknown>),
	useGetEvaluationReportQuery: () => ({ data: fx.report, isLoading: false, isError: false, isFetching: false, refetch: () => undefined }),
}));

const { PastTest, PastTestList } = await import("./failureTestsView");

function ev(id: string, status: string, faults: string[], extra: Partial<Evaluation> = {}): Evaluation {
	return {
		id,
		status,
		created_at: "2026-11-22T00:00:00Z",
		spec: { name: `test ${id}`, mode: "score", arms: [{ label: "A", agent: "codex", model: "m" }], faults },
		children: [{ benchmark: "hd-harbor-bfcl", experiment_id: `e_${id}`, status }],
		...extra,
	} as unknown as Evaluation;
}

const reportWith = (drop: number) =>
	({
		arms: [{ label: "A", agent: "codex", model: "m" }],
		reliability: [{ fault: "model-down", arms: { A: { score: 0.6, drop } } }],
	}) as unknown as Report;

describe("PastTest sign (N126)", () => {
	it("shows a gain as a plus, in green, against clean", () => {
		fx.report = reportWith(0.04);
		const html = render(<PastTest ev={ev("ev_g", "done", ["clean", "model-down"])} />);
		expect(html).toContain("+4 pts vs clean");
		expect(html).not.toContain("-4 pts");
		expect(html).toContain("text-chart-success-ink");
		expect(html).not.toContain("text-chart-error-ink");
	});

	it("shows a loss as a minus, in red", () => {
		fx.report = reportWith(-0.67);
		const html = render(<PastTest ev={ev("ev_l", "done", ["clean", "model-down"])} />);
		expect(html).toContain("-67 pts vs clean");
		expect(html).toContain("text-chart-error-ink");
	});
});

describe("PastTest faults and empty results (N127)", () => {
	it("names the faults and why a cancelled test has no results", () => {
		fx.report = { arms: [{ label: "A" }], reliability: [] };
		const html = render(<PastTest ev={ev("ev_c", "cancelled", ["clean", "model-down"])} />);
		expect(html).toContain("Faults: Model goes down");
		expect(html).toContain("Cancelled before any fault run finished.");
		expect(html).not.toContain("No fault results in this evaluation.");
	});

	it("says why a failed or finished test has no results", () => {
		fx.report = { arms: [{ label: "A" }], reliability: [] };
		expect(render(<PastTest ev={ev("ev_f", "failed", ["clean", "model-down"])} />)).toContain("Failed before any fault run finished.");
		expect(render(<PastTest ev={ev("ev_d", "done", ["clean", "model-down"])} />)).toContain("No fault run of this agent finished.");
		const skipped = ev("ev_s", "done", ["clean", "model-down"]);
		skipped.children = [{ ...skipped.children[0], faults_skipped: [{ fault: "model-down", reason: "no model" }] }];
		expect(render(<PastTest ev={skipped} />)).toContain("None of the chosen faults applied to these benchmarks.");
	});
});

describe("PastTestList (N127)", () => {
	it("hides stopped tests by default and says how many", () => {
		fx.report = reportWith(-0.1);
		const html = render(
			<PastTestList evs={[ev("ev_ok", "done", ["clean", "model-down"]), ev("ev_x", "cancelled", ["clean", "model-flaky"])]} />,
		);
		expect(html).toContain('data-testid="ft-past-list"');
		expect(html).toContain('data-testid="ft-past-ev_ok"');
		expect(html).not.toContain('data-testid="ft-past-ev_x"');
		expect(html).toContain("1 stopped test hidden");
		expect(html).toContain("Hide stopped tests");
	});

	it("keeps a stopped test that has a finished benchmark", () => {
		fx.report = reportWith(-0.1);
		const stopped = ev("ev_p", "cancelled", ["clean", "model-down"]);
		stopped.children = [...stopped.children.map((c) => ({ ...c, status: "done" }))];
		const html = render(<PastTestList evs={[stopped]} />);
		expect(html).toContain('data-testid="ft-past-ev_p"');
		expect(html).not.toContain("stopped test");
	});
});